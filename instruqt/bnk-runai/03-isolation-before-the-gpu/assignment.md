---
slug: isolation-before-the-gpu
id: 7mtupreqvpsj
type: challenge
title: Isolation before the GPU
teaser: One tenant floods its endpoint. BNK refuses the excess with a 429 before it
  reaches the GPU both tenants share, and the other tenant never notices.
notes:
- type: image
  url: ../assets/notes-03.png
tabs:
- id: bhh8k4kivz5u
  title: Terminal
  type: terminal
  hostname: control
- id: dnxktke17biy
  title: Topology
  type: service
  hostname: control
  path: /topology.html
  port: 3002
- id: eykzarcwdwmo
  title: Terminal 2
  type: terminal
  hostname: control
- id: culm8euqfpu6
  title: Grafana
  type: service
  hostname: control
  path: /d/one-tenant-two-planes?orgId=1&kiosk&hideLogo&refresh=5s
  port: 3000
- id: kykrf8yyuawt
  title: Run:ai UI
  type: service
  hostname: control
  port: 3001
- id: lzkutf2hflrw
  title: Lab code
  type: code
  hostname: control
  path: /root/lab
difficulty: intermediate
timelimit: 1800
enhanced_loading: null
---
# Isolation before the GPU

`tenant-a` and `tenant-b` now share one GPU, half each. The GPU has no idea which tenant a request
came from. It serves whatever arrives. So if `tenant-b` sends ten times its fair load, `tenant-a`'s
requests end up waiting behind it.

Run:ai already decides how much GPU each project deserves. In this challenge BNK enforces that share on
the request path, before anything reaches the GPU. Each tenant keeps its share, and whatever a tenant
sends beyond it is refused at the edge.

![Isolation at the BNK edge](../assets/03-isolation.png)

---

## Before you start: open Grafana

The **Grafana** tab is where you'll see the isolation happen, so keep it handy while the load runs.
These are the panels to watch:

| Panel | Where | What to watch |
|---|---|---|
| **Requests by department (BNK)** | top row | the total jumps when tenant-b floods |
| **Project tenant-a → Requests/s and 429s (BNK)** | tenant-a row | a flat line of 200s and **no 429s** |
| **Project tenant-b → Requests/s and 429s (BNK)** | tenant-b row | 200s capped near 10/s, and a large **429** line above it |
| **vLLM: requests running / waiting** | each project row | tenant-a's waiting queue stays flat |

Each project has its own row further down the board. It refreshes every 5 seconds, and setting the
time picker to **Last 5 minutes** makes the change easier to see.

---

## Step 1 · Start tenant-a's normal traffic

In **Terminal 2**, run steady load against `tenant-a` for three minutes. Leave it running:

```bash,run
labctl load steady --tenant tenant-a --seconds 180
```

Keep an eye on its success rate and p95 latency. That's your baseline.

---

## Step 2 · Overload tenant-b

Back in **Terminal**, send `tenant-b` far more than its 10 requests per second:

```bash,run
labctl load overload --tenant tenant-b --seconds 60
```

**You should see:** most of tenant-b's requests come back as **429**.

Look at one refusal yourself:

```bash,run
VIP=$(kubectl get gateway -n f5-bnk tenant-b -o jsonpath='{.status.addresses[0].value}')
for i in $(seq 1 30); do curl -s -o /dev/null http://$VIP:8000/v1/models & done; wait
curl -si http://$VIP:8000/v1/models | sed -n '1p;/x-bnk-tenant/Ip;/retry-after/Ip;$p'
```

The body is JSON with code `tenant/rate-limited` and names the Run:ai project.

That 429 came from the tenant's iRule in **TMM**, BNK's data plane. The request never reached a model
server, so it cost no GPU time at all. This is the rule that answered it:

```bash,run
kubectl get f5-big-cne-irules -n f5-bnk tenant-b-limit -o jsonpath='{.spec.iRule}'
```

Now switch to **Terminal 2**. `tenant-a`'s p95 hasn't moved.

**In Grafana**, compare the two project rows. tenant-b has a large 429 line and tenant-a has none. A
tenant that goes over its quota only slows itself down.

> The model servers are vLLM simulators whose latency grows with load, so the effect on a shared GPU
> is modelled, not measured on real silicon. The 429s and where they happen are real BNK behaviour.

---

## Step 3 · The Endpoint Picker inside a tenant

The rate limit decides how much traffic a tenant gets. Once a request is inside the tenant, a second
question comes up: which replica should serve it? BNK's Endpoint Picker answers that. To see it work,
give `tenant-a` a second replica:

```bash,run
labctl model deploy tenant-a --replicas 2 --fraction 0.5
NS=$(kubectl get projects.kai.resources tenant-a -o jsonpath='{.status.namespace}')
kubectl get pods -n "$NS" -o wide -l app.kubernetes.io/part-of=model-server
```

The controller created the picker for this tenant as an `F5EPP` object. It scores each replica on two
things: how long its queue is, and how much of the prompt it already has in its KV cache.

```bash,run
kubectl get f5-epps -n "$NS" model-pool-epp -o jsonpath='{.spec.eppConfig}' | jq .
kubectl get inferencepools.inference.networking.k8s.io -n "$NS" model-pool -o jsonpath='{.spec.endpointPickerRef}' | jq .
```

Send load where many requests share a long prompt prefix:

```bash,run
labctl load prefix-heavy --tenant tenant-a --seconds 45
```

Requests that start the same way keep going to the replica that has already seen that start. It still
has those tokens cached, so it skips recomputing them. On a real GPU that's prefill time saved.

Nothing here is specific to this lab. The picker works on Run:ai inference pods as they are. BNK already
ships it, and all the tenancy controller does is set one up for each project.

**In Grafana**, look at the tenant-a row. **vLLM: prefix-cache hit rate** climbs toward 100%: almost every
prompt start was already cached where it landed. **vLLM: requests per replica** shows where BNK sent each
request. With only a few distinct prompt starts, most of them can stick to one replica. That's the picker
keeping a prefix where its cache is, not a replica sitting idle.

---

## Step 4 · One tenant cannot use another's front door

Rate limits protect capacity. The Gateway protects identity. Each tenant's Gateway only accepts routes
from that tenant's own namespace.

```bash,run
kubectl get gateway -n f5-bnk tenant-a -o jsonpath='{.spec.listeners[0].allowedRoutes}' | jq .
```

**You should see:** `from: Selector`, matching only `runai-tenant-a`.

So try to break it. From `tenant-b`'s namespace, attach a route to `tenant-a`'s Gateway:

```bash,run
NSB=$(kubectl get projects.kai.resources tenant-b -o jsonpath='{.status.namespace}')
cat <<YAML | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: hijack-tenant-a
  namespace: $NSB
spec:
  parentRefs:
  - name: tenant-a
    namespace: f5-bnk
    sectionName: http
  rules:
  - matches:
    - path: {type: PathPrefix, value: /v1}
    backendRefs:
    - group: inference.networking.k8s.io
      kind: InferencePool
      name: model-pool
      port: 8000
YAML
echo "waiting for BNK to judge the route..."
for i in $(seq 1 30); do
  st=$(kubectl get httproute -n "$NSB" hijack-tenant-a -o jsonpath='{range .status.parents[*].conditions[*]}{.type}={.status} {.reason}{"\n"}{end}')
  [ -n "$st" ] && break; sleep 2
done
echo "$st"
```

**You should see:** `Accepted=False NotAllowedByListeners`. BNK refused the route, so `tenant-b` can't
publish anything behind `tenant-a`'s address. Compare that with `tenant-a`'s own route:

```bash,run
NS=$(kubectl get projects.kai.resources tenant-a -o jsonpath='{.status.namespace}')
kubectl get httproute -n "$NS" tenant-route -o jsonpath='{range .status.parents[*].conditions[*]}{.type}={.status}{"\n"}{end}'
```

Leave the rejected route in place for the check, or delete it when you are done:
`kubectl delete httproute -n $NSB hijack-tenant-a`.

Click **Check** when you have seen tenant-b throttled.
