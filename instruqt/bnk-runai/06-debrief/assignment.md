---
slug: debrief
id: tvwvrxmzsupm
type: challenge
title: What you built, and what's next
teaser: A recap of the integration, what in this lab is real and what is simulated,
  and what changes on a real BlueField-3 DPU.
notes:
- type: image
  url: ../assets/notes-06.png
tabs:
- id: irmf0r2yf5ja
  title: Terminal
  type: terminal
  hostname: control
- id: jdnyh35xvg8q
  title: Topology
  type: service
  hostname: control
  path: /topology.html
  port: 3002
- id: oeesamh9bpil
  title: Grafana
  type: service
  hostname: control
  path: /d/one-tenant-two-planes?orgId=1&kiosk&hideLogo&refresh=5s
  port: 3000
- id: felupoweknec
  title: Run:ai UI
  type: service
  hostname: control
  port: 3001
- id: hh297xeyexj2
  title: Lab code
  type: code
  hostname: control
  path: /root/lab
difficulty: basic
timelimit: 900
enhanced_loading: null
---
![What you built](../assets/06-recap.png)

# What you built

You defined two tenants in Run:ai and never configured BNK. From each Run:ai project the controller
built a front door, a route to the tenant's model pool, an Endpoint Picker and a rate limit taken
from the GPU quota. When a project went away, so did its BNK config.

Then you saw why that matters on shared GPUs. `tenant-b` was refused at the edge with a 429 once it
went over its share, and `tenant-a`, on the same GPU, kept its latency. Tokens were counted per
project at the edge and written back onto the Run:ai Project and its department.

Finally, NVIDIA Dynamo ran as one more Run:ai project, and BNK went a step further than a front door.
Its Endpoint Picker listened to the KV-cache events Dynamo's workers publish and sent each request to
the worker that already held its prompt.

One last look at the whole thing, from both sides.

Scroll through the **Grafana** board one more time too. Every project's GPU share, requests, 429s and
tokens are on one screen.

```bash,run
labctl status
kubectl get projects.kai.resources -o custom-columns='PROJECT:.metadata.name,DEPARTMENT:.spec.parent,NAMESPACE:.status.namespace,PHASE:.status.phase'
kubectl get gateway -n f5-bnk -o wide -L tenancy.f5.com/project
kubectl -n bnk-runai-tenancy get cm bnk-runai-usage -o jsonpath='{.data}' | jq .
```

---

## What is real, and what is simulated

| | |
|---|---|
| **Real** | BNK 2.4 (TMM and its controllers), open-source Run:ai and its scheduler, Kubernetes, the tenancy controller and token meter, the 429s, the Endpoint Picker, the metrics path, the Dynamo operator and its KV-cache events, BNK's Endpoint Picker in Dynamo mode |
| **Simulated** | the GPUs (NVIDIA fake-gpu-operator), the model servers (vLLM simulator and Dynamo Mocker, so tokens and latencies are modelled), the BlueField DPU (a node joined to the GPU node by a tunnel) |
| **Not shown** | GPU memory isolation, DPU offload, host-CPU savings |

---

## What changes with a real BlueField-3

On BlueField-3, TMM runs on the DPU itself rather than on a server core. The intent is that ingress,
rate limiting and metering stop competing with the inference server for host CPU, and refused
requests never touch the host at all. That is the design, but it isn't measured here. Figures for it
come from F5's BlueField-3 testing, not from this lab.

Nothing in the integration changes when that happens. The controller writes Kubernetes objects, and
where TMM runs is up to BNK.

---

## The open-source pieces

- **Tenancy controller and token meter**: Apache 2.0, in the **Lab code** tab under `controller/`. It
  watches `kai.resources/v1alpha1` Projects and writes standard Gateway API and BNK objects.
- **Upstream**: a docs pull request and an issue to `dsx-ai-factory/run-ai` are planned, describing
  how a service plane can consume Run:ai's tenant model. Not yet filed.

---

## Optional stretch

The same objects are a natural surface for an AI agent. The repo ships a stateless MCP server,
`bnk-tenancy-mcp`, that answers "what is this project's front door and how many tokens has it used",
next to NVIDIA's own Run:ai MCP server. See `docs/mcp.md` in the **Lab code** tab. Not part of the graded
challenges.

Click **Check** to finish.
