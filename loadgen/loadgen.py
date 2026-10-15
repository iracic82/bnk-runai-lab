#!/usr/bin/env python3
"""Open-loop OpenAI chat load against one BNK tenant VIP. Python 3 stdlib only.

Sends a fixed number of requests per second (open loop: a slow or refusing server does not slow the
sender down, which is what an overloaded tenant looks like), and prints one line per second with the
count of 200s, 429s and everything else plus p50/p95 latency of the requests that finished in that
second. A summary follows at the end; --json prints it as JSON on stdout.

Profiles:
  steady        a tenant's normal traffic, well under its BNK rate limit
  overload      far above a 0.5-GPU tenant's limit (10 req/s), so most answers are BNK 429s
  prefix-heavy  long shared system prompts (few distinct prefixes) so the Endpoint Picker can route
                requests to the replica that already holds the prefix in its KV cache

  loadgen.py --url http://172.16.201.110:8000 --profile overload --seconds 60
"""
import argparse
import json
import random
import signal
import sys
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

PROFILES = {
    # rps, max_tokens, prompt style
    "steady": {"rps": 8.0, "max_tokens": 32, "prompt": "short"},
    "overload": {"rps": 40.0, "max_tokens": 32, "prompt": "short"},
    "prefix-heavy": {"rps": 6.0, "max_tokens": 16, "prompt": "prefix"},
}

QUESTIONS = [
    "Summarise the main risk in two sentences.",
    "What should the on-call engineer check first?",
    "List three follow-up questions for the customer.",
    "Translate the key finding into plain language.",
    "Which metric would show this problem earliest?",
    "Write a one-line status update for the incident channel.",
    "What is the cheapest mitigation?",
    "Explain the root cause to a new team member.",
]

TOPICS = ["GPU scheduling", "inference gateways", "KV cache reuse", "rate limiting",
          "multi-tenant clusters", "DPU offload", "token metering", "model rollouts"]


def shared_prefix(i, words=1100):
    """A long, deterministic system prompt. Few distinct ones, so many requests share a prefix."""
    rnd = random.Random(1000 + i)
    vocab = ("the tenant platform routes inference traffic across accelerator pools while quota "
             "policy decides which project may use how much of the shared estate and the gateway "
             "enforces that share before any request reaches a model server").split()
    body = " ".join(rnd.choice(vocab) for _ in range(words))
    return f"You are runbook assistant #{i}. Reference document follows.\n{body}\nEnd of reference."


PREFIXES = None


def payload(profile, model, seq):
    global PREFIXES
    if profile["prompt"] == "prefix":
        if PREFIXES is None:
            PREFIXES = [shared_prefix(i) for i in range(profile.get("prefixes", 4))]
        msgs = [{"role": "system", "content": PREFIXES[seq % len(PREFIXES)]},
                {"role": "user", "content": f"[{seq}] {random.choice(QUESTIONS)}"}]
    else:
        msgs = [{"role": "user",
                 "content": f"[{seq}] In one paragraph, explain {random.choice(TOPICS)}. {random.choice(QUESTIONS)}"}]
    body = {"messages": msgs, "max_tokens": profile["max_tokens"], "stream": False}
    if model:
        body["model"] = model
    return json.dumps(body).encode()


def discover_model(url, timeout, attempts=10):
    """The model id the tenant serves. A 429 here means this second's rate-limit budget is spent
    (often by the previous command), so wait Retry-After and ask again rather than give up."""
    for _ in range(attempts):
        try:
            with urllib.request.urlopen(url + "/v1/models", timeout=timeout) as r:
                return (json.load(r).get("data") or [{}])[0].get("id")
        except urllib.error.HTTPError as e:
            if e.code != 429:
                return None
            try:
                wait = float(e.headers.get("Retry-After") or 1)
            except ValueError:
                wait = 1.0
            time.sleep(min(max(wait, 0.5), 5))
        except Exception:
            return None
    return None


def pct(values, p):
    if not values:
        return None
    v = sorted(values)
    k = min(len(v) - 1, max(0, int(round(p / 100.0 * (len(v) - 1)))))
    return v[k]


def fmt(s):
    return "   -  " if s is None else f"{s:5.2f}s"


class Stats:
    def __init__(self):
        self.lock = threading.Lock()
        self.buckets = {}   # completion second -> {"200":n,"429":n,"other":n,"err":n,"lat":[...]}
        self.tenant_header = {}
        self.tokens = {"prompt": 0, "completion": 0}
        self.inflight = 0

    def record(self, sec, code, lat, tenant=None, usage=None):
        with self.lock:
            b = self.buckets.setdefault(sec, {"200": 0, "429": 0, "other": 0, "err": 0, "lat": []})
            if code == 200:
                b["200"] += 1
                b["lat"].append(lat)
            elif code == 429:
                b["429"] += 1
            elif code is None:
                b["err"] += 1
            else:
                b["other"] += 1
            if tenant:
                self.tenant_header[tenant] = self.tenant_header.get(tenant, 0) + 1
            if usage:
                self.tokens["prompt"] += int(usage.get("prompt_tokens") or 0)
                self.tokens["completion"] += int(usage.get("completion_tokens") or 0)


def one_request(url, data, timeout, stats, t0):
    start = time.monotonic()
    code, tenant, usage = None, None, None
    req = urllib.request.Request(url + "/v1/chat/completions", data=data,
                                 headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            code = r.status
            tenant = r.headers.get("X-BNK-Tenant")
            try:
                usage = json.loads(r.read()).get("usage")
            except ValueError:
                pass
    except urllib.error.HTTPError as e:
        code = e.code
        tenant = e.headers.get("X-BNK-Tenant") if e.headers else None
        e.read()
    except Exception:
        code = None
    end = time.monotonic()
    with stats.lock:
        stats.inflight -= 1
    stats.record(int(end - t0), code, end - start, tenant, usage)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--url", required=True, help="base URL of the tenant VIP, e.g. http://172.16.201.110:8000")
    ap.add_argument("--profile", choices=sorted(PROFILES), default="steady")
    ap.add_argument("--seconds", type=int, default=30)
    ap.add_argument("--rps", type=float, help="override the profile's request rate")
    ap.add_argument("--max-tokens", type=int, help="override the profile's max_tokens")
    ap.add_argument("--model", help="model id to send (default: read from /v1/models)")
    ap.add_argument("--prefixes", type=int, default=4, help="distinct shared prefixes (prefix-heavy)")
    ap.add_argument("--timeout", type=float, default=30.0, help="per-request timeout in seconds")
    ap.add_argument("--concurrency", type=int, default=0, help="worker threads (default: rps x 15, max 600)")
    ap.add_argument("--label", default="", help="shown on every line, e.g. the tenant name")
    ap.add_argument("--json", action="store_true", help="print the summary as JSON on stdout")
    a = ap.parse_args()

    profile = dict(PROFILES[a.profile])
    if a.rps:
        profile["rps"] = a.rps
    if a.max_tokens:
        profile["max_tokens"] = a.max_tokens
    profile["prefixes"] = max(1, a.prefixes)
    url = a.url.rstrip("/")
    rps = max(0.1, profile["rps"])
    log = sys.stderr if a.json else sys.stdout
    model = a.model or discover_model(url, min(5.0, a.timeout))
    if not model:
        print(f"could not read the model from {url}/v1/models: is the tenant's model server up and its "
              f"route programmed? Pass --model to skip discovery.", file=sys.stderr, flush=True)
        return 2
    workers = a.concurrency or int(min(600, max(16, rps * 15)))
    tag = f"[{a.label}] " if a.label else ""
    print(f"{tag}profile={a.profile} rps={rps:g} seconds={a.seconds} max_tokens={profile['max_tokens']} "
          f"model={model or '(none)'} url={url}", file=log, flush=True)

    stats = Stats()
    pool = ThreadPoolExecutor(max_workers=workers)
    stop = threading.Event()
    t0 = time.monotonic()
    sent = {"n": 0}

    def sender():
        interval = 1.0 / rps
        nxt = t0
        while not stop.is_set() and nxt < t0 + a.seconds:
            now = time.monotonic()
            if now < nxt:
                time.sleep(min(nxt - now, 0.05))
                continue
            data = payload(profile, model, sent["n"])
            with stats.lock:
                stats.inflight += 1
            pool.submit(one_request, url, data, a.timeout, stats, t0)
            sent["n"] += 1
            nxt += interval

    th = threading.Thread(target=sender, daemon=True)
    th.start()
    per_second = []
    printed = 0
    try:
        while True:
            time.sleep(0.2)
            elapsed = time.monotonic() - t0
            # print every fully elapsed second (completions are bucketed by completion time)
            while printed < int(elapsed) and (printed < a.seconds or stats.inflight > 0):
                with stats.lock:
                    b = stats.buckets.get(printed, {"200": 0, "429": 0, "other": 0, "err": 0, "lat": []})
                    lat = list(b["lat"])
                    inflight = stats.inflight
                row = {"t": printed + 1, "ok": b["200"], "throttled": b["429"], "other": b["other"],
                       "errors": b["err"], "p50": pct(lat, 50), "p95": pct(lat, 95)}
                per_second.append(row)
                print(f"{tag}t={row['t']:4d}s  200={row['ok']:4d}  429={row['throttled']:4d}  "
                      f"other={row['other']:3d}  err={row['errors']:3d}  p50={fmt(row['p50'])}  "
                      f"p95={fmt(row['p95'])}  inflight={inflight}", file=log, flush=True)
                printed += 1
            if elapsed >= a.seconds and (stats.inflight == 0 or elapsed > a.seconds + a.timeout + 1):
                break
    except KeyboardInterrupt:
        stop.set()
    stop.set()
    pool.shutdown(wait=False, cancel_futures=True)

    with stats.lock:
        all_lat = [x for b in stats.buckets.values() for x in b["lat"]]
        tot = {k: sum(b[k] for b in stats.buckets.values()) for k in ("200", "429", "other", "err")}
        hdr = dict(stats.tenant_header)
        tokens = dict(stats.tokens)
    done = sum(tot.values())
    summary = {
        "profile": a.profile, "url": url, "label": a.label, "model": model, "rps_target": rps,
        "seconds": a.seconds, "sent": sent["n"], "completed": done,
        "ok": tot["200"], "throttled": tot["429"], "other": tot["other"], "errors": tot["err"],
        "throttled_ratio": round(tot["429"] / done, 4) if done else 0.0,
        "ok_rps": round(tot["200"] / max(1, a.seconds), 2),
        "latency_ok_s": {"p50": pct(all_lat, 50), "p95": pct(all_lat, 95), "p99": pct(all_lat, 99)},
        "x_bnk_tenant": hdr, "tokens": tokens, "per_second": per_second,
    }
    print(f"{tag}summary: sent={summary['sent']} 200={tot['200']} 429={tot['429']} other={tot['other']} "
          f"err={tot['err']} ({summary['throttled_ratio'] * 100:.0f}% throttled)  "
          f"p50={fmt(summary['latency_ok_s']['p50'])} p95={fmt(summary['latency_ok_s']['p95'])}  "
          f"tokens prompt={tokens['prompt']} completion={tokens['completion']}  "
          f"X-BNK-Tenant={','.join(hdr) or 'none'}", file=log, flush=True)
    if a.json:
        print(json.dumps(summary))
    return 0 if done else 1


if __name__ == "__main__":
    signal.signal(signal.SIGPIPE, signal.SIG_DFL)  # `| head` ends the run quietly instead of a traceback
    sys.exit(main())
