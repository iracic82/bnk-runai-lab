#!/usr/bin/env python3
"""An AI agent that runs the AI factory through MCP, with any OpenAI-compatible LLM.

It connects to two MCP servers: NVIDIA's runai-mcp-server (Run:ai projects) and bnk-tenancy-mcp
(what BNK built for a project). Their tools become function definitions for the LLM, the LLM decides
which to call, and this loop calls them over MCP Streamable HTTP. Nothing here is specific to one LLM:
set LLM_BASE_URL, LLM_MODEL and LLM_API_KEY (defaults: NVIDIA's hosted API).

A tool call that changes something (manage_org_unit with dryRun=false) is shown to the human first
and runs only after they type y. Run:ai's own dry-run preview is what the agent shows before asking.

  agent.py "Create a project team-x with 1 GPU and tell me its endpoint"
  agent.py            # interactive

Standard library only; run on the control node after platform/mcp/install.sh.
"""
import json
import os
import ssl
import subprocess
import sys
import urllib.error
import urllib.request

PROTO = "2025-06-18"
LLM_BASE_URL = os.environ.get("LLM_BASE_URL", "https://integrate.api.nvidia.com/v1").rstrip("/")
LLM_MODEL = os.environ.get("LLM_MODEL", "nvidia/nemotron-3.5-super-vl-120b-a12b")
def _key_from_file(path="/root/.config/lab/llm.env"):
    # Track setup writes the Instruqt secret here (mode 600), so a fresh shell finds it.
    try:
        for line in open(path):
            if line.startswith("LLM_API_KEY="):
                return line.split("=", 1)[1].strip()
    except OSError:
        pass
    return ""


LLM_API_KEY = os.environ.get("LLM_API_KEY") or os.environ.get("NVIDIA_API_KEY") or _key_from_file()
# Nemotron reasons before answering when thinking is on; off keeps each tool step fast. LLM_THINKING=1 turns it on.
LLM_EXTRA = {"chat_template_kwargs": {"enable_thinking": os.environ.get("LLM_THINKING") == "1"}} \
    if "nemotron" in LLM_MODEL else {}
DOMAIN = os.environ.get("RUNAI_DOMAIN", "runai.lab")
CA_FILE = os.environ.get("RUNAI_CA_FILE", "/etc/runai/ca.crt")
AUTO_APPROVE = os.environ.get("AGENT_AUTO_APPROVE") == "1"   # for the track's solve script only

# Only the tools this job needs, so a 27-tool server does not crowd the model's context.
RUNAI_TOOLS = {"list_projects", "list_departments", "list_node_pools", "manage_org_unit"}

SYSTEM = """You operate an AI factory: NVIDIA Run:ai manages GPU quota per project, and F5 BIG-IP Next for
Kubernetes (BNK) builds each project's front door (VIP, rate limit, token metering) on its own once the
project exists. You act only through the tools you are given.

To create a project: call list_node_pools and list_departments to find the clusterId and the 'default'
department id, then call manage_org_unit with action "create", type "project", the name, clusterId,
parentId and resources {"nodePools":[{"nodePoolName":"default","gpuDeservedQuota":N}]}, first with
dryRun true. Then call it again with dryRun false and confirm true; a human approves that call.
BNK needs a few seconds after that: call get_tenant_front_door until it reports a VIP, then answer
with the endpoint, the rate limit and the token count. Never invent values; report only what tools return.
Keep the final answer short."""

BOLD, DIM, CYAN, YEL, OFF = "\033[1m", "\033[2m", "\033[36m", "\033[33m", "\033[0m"


def kubectl(*args):
    return subprocess.run(["kubectl", *args], check=True, capture_output=True, text=True).stdout.strip()


def http_json(url, body, headers, timeout=120, ctx=None):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=timeout, context=ctx) as r:
        return r.headers, r.read().decode()


class MCP:
    """One MCP server over Streamable HTTP. Handles JSON and event-stream answers and session ids."""

    def __init__(self, name, url, token, stateful):
        self.name, self.url, self.token, self.session, self.id = name, url, token, None, 0
        if stateful:
            self.call("initialize", {"protocolVersion": PROTO, "capabilities": {},
                                     "clientInfo": {"name": "bnk-runai-agent", "version": "1.0"}})
            self.notify("notifications/initialized")

    def _headers(self):
        h = {"Authorization": f"Bearer {self.token}", "Content-Type": "application/json",
             "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": PROTO}
        if self.session:
            h["Mcp-Session-Id"] = self.session
        return h

    def notify(self, method):
        try:
            http_json(self.url, {"jsonrpc": "2.0", "method": method}, self._headers(), timeout=30)
        except urllib.error.HTTPError:
            pass

    def call(self, method, params):
        self.id += 1
        hdrs, text = http_json(self.url, {"jsonrpc": "2.0", "id": self.id, "method": method, "params": params},
                               self._headers())
        self.session = hdrs.get("Mcp-Session-Id") or self.session
        if "text/event-stream" in (hdrs.get("Content-Type") or ""):
            msgs = [json.loads(l[5:]) for l in text.splitlines() if l.startswith("data:") and l[5:].strip()]
            msg = [m for m in msgs if m.get("id") == self.id][-1]
        else:
            msg = json.loads(text)
        if "error" in msg:
            raise RuntimeError(f"{self.name} {method}: {msg['error']}")
        return msg["result"]

    def tools(self):
        return self.call("tools/list", {}).get("tools", [])

    def run(self, name, args):
        r = self.call("tools/call", {"name": name, "arguments": args})
        text = r.get("structuredContent") or "\n".join(c.get("text", "") for c in r.get("content", []))
        out = text if isinstance(text, str) else json.dumps(text)
        return ("ERROR: " + out) if r.get("isError") else out


def runai_token():
    ctx = ssl.create_default_context(cafile=CA_FILE)
    _, text = http_json(f"https://{DOMAIN}/api/v1/token",
                        {"grantType": "password", "username": os.environ.get("RUNAI_ADMIN_EMAIL", "admin@acme.lab"),
                         "password": os.environ.get("RUNAI_ADMIN_PASSWORD", "Lab-Admin-1!")},
                        {"Content-Type": "application/json"}, timeout=20, ctx=ctx)
    d = json.loads(text)
    return d.get("accessToken") or d["access_token"]


def connect():
    runai_url = os.environ.get("RUNAI_MCP_URL") or \
        f"http://{kubectl('-n', 'runai-mcp', 'get', 'svc', 'runai-mcp-server', '-o', 'jsonpath={.spec.clusterIP}')}:8080/mcp"
    bnk_url = os.environ.get("BNK_MCP_URL") or \
        f"http://{kubectl('-n', 'bnk-runai-tenancy', 'get', 'svc', 'bnk-tenancy-mcp', '-o', 'jsonpath={.spec.clusterIP}')}:8090/mcp"
    import base64
    bnk_token = os.environ.get("BNK_MCP_TOKEN") or base64.b64decode(
        kubectl("-n", "bnk-runai-tenancy", "get", "secret", "bnk-tenancy-mcp-token", "-o", "jsonpath={.data.token}")).decode()
    servers = [MCP("Run:ai MCP", runai_url, runai_token(), stateful=True),
               MCP("BNK MCP", bnk_url, bnk_token, stateful=False)]
    route, functions = {}, []
    for s in servers:
        for t in s.tools():
            if s.name == "Run:ai MCP" and t["name"] not in RUNAI_TOOLS:
                continue
            route[t["name"]] = s
            functions.append({"type": "function", "function": {
                "name": t["name"], "description": (t.get("description") or "")[:1000],
                "parameters": t.get("inputSchema") or {"type": "object", "properties": {}}}})
    for s in servers:
        names = [n for n, srv in route.items() if srv is s]
        print(f"{DIM}connected to {s.name}: {', '.join(names)}{OFF}")
    return route, functions


def llm(messages, functions):
    _, text = http_json(f"{LLM_BASE_URL}/chat/completions",
                        {"model": LLM_MODEL, "messages": messages, "tools": functions, "tool_choice": "auto",
                         "temperature": 0.1, "max_tokens": 2048, **LLM_EXTRA},
                        {"Authorization": f"Bearer {LLM_API_KEY}", "Content-Type": "application/json"})
    return json.loads(text)["choices"][0]["message"]


def is_write(name, args):
    return name == "manage_org_unit" and not args.get("dryRun", True)


def approve(name, args):
    print(f"{YEL}{BOLD}  the agent wants to apply a change:{OFF} {name} {json.dumps(args)}")
    if AUTO_APPROVE:
        print(f"{YEL}  approved (AGENT_AUTO_APPROVE=1){OFF}")
        return True
    try:
        return input(f"{YEL}  approve? [y/N] {OFF}").strip().lower() == "y"
    except EOFError:
        return False


def turn(messages, route, functions, max_steps=16):
    for _ in range(max_steps):
        msg = llm(messages, functions)
        messages.append({k: v for k, v in msg.items() if k in ("role", "content", "tool_calls") and v is not None})
        calls = msg.get("tool_calls") or []
        if not calls:
            print(f"\n{BOLD}agent:{OFF} {msg.get('content', '').strip()}\n")
            return
        for c in calls:
            name = c["function"]["name"]
            try:
                args = json.loads(c["function"].get("arguments") or "{}")
            except json.JSONDecodeError:
                args = {}
            server = route.get(name)
            print(f"{CYAN}  -> {server.name if server else '?'}: {name}{OFF} {DIM}{json.dumps(args)[:200]}{OFF}")
            if server is None:
                result = f"ERROR: unknown tool {name}"
            elif is_write(name, args) and not approve(name, args):
                result = "ERROR: the human declined this change. Do not retry it; say so in your answer."
            else:
                try:
                    result = server.run(name, args)
                except Exception as e:  # report tool failures to the model instead of crashing the loop
                    result = f"ERROR: {e}"
            print(f"{DIM}     {result[:220].replace(chr(10), ' ')}{OFF}")
            messages.append({"role": "tool", "tool_call_id": c["id"], "content": result[:8000]})
    print(f"{YEL}stopped after {max_steps} steps{OFF}")


def main():
    if not LLM_API_KEY:
        sys.exit("LLM_API_KEY is not set. Without an LLM, run the scripted version: bash platform/mcp/demo.sh <project> 1")
    print(f"{BOLD}AI factory agent{OFF} {DIM}(LLM {LLM_MODEL} at {LLM_BASE_URL}){OFF}")
    route, functions = connect()
    messages = [{"role": "system", "content": SYSTEM}]
    prompts = [" ".join(sys.argv[1:])] if len(sys.argv) > 1 else None
    while True:
        if prompts is not None:
            if not prompts:
                return
            q = prompts.pop()
            print(f"\n{BOLD}you:{OFF} {q}")
        else:
            try:
                q = input(f"{BOLD}you:{OFF} ").strip()
            except EOFError:
                return
            if q in ("", "exit", "quit"):
                return
        messages.append({"role": "user", "content": q})
        turn(messages, route, functions)


if __name__ == "__main__":
    main()
