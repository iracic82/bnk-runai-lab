---
slug: debrief
type: challenge
title: What you built, and what's next
teaser: A recap of the integration, what in this lab is real and what is simulated, and what changes on a real BlueField-3 DPU.
notes:
- type: image
  url: ../assets/notes-06.png
tabs:
- title: Terminal
  type: terminal
  hostname: control
- title: Grafana
  type: service
  hostname: control
  path: /d/one-tenant-two-planes?orgId=1&kiosk
  port: 3000
- title: Run:ai UI
  type: service
  hostname: control
  port: 3001
- title: Lab code
  type: code
  hostname: control
  path: /root/lab
difficulty: basic
timelimit: 900
enhanced_loading:
---
![What you built](../assets/06-recap.png)

# What you built

You defined two tenants in Run:ai and never configured BNK. From each Run:ai project the controller
built a front door, a route to the tenant's model pool, an Endpoint Picker and a rate limit taken
from the GPU quota. When a project went away, so did its BNK config.

Then you saw why that matters on shared GPUs. `tenant-b` was refused at the edge with a 429 once it
went over its share, and `tenant-a`, on the same GPU, kept its latency. Tokens were counted per
project at the edge and written back onto the Run:ai Project and its department.

One last look at the whole thing, from both sides:

```bash,run
labctl status
kubectl get projects.kai.resources -o custom-columns='PROJECT:.metadata.name,DEPARTMENT:.spec.parent,NAMESPACE:.status.namespace,TOKENS:.metadata.annotations.tenancy\.f5\.com/tokens-total'
kubectl get gateway -n f5-bnk -L tenancy.f5.com/project,tenancy.f5.com/department
```

# What is real, and what is simulated

**Real:** BNK 2.4 (TMM data plane and its controllers), the open-source Run:ai control plane and
scheduler, Kubernetes, the tenancy controller and token meter, the 429s, the Endpoint Picker and the
metrics path.

**Simulated:** the GPUs (NVIDIA's fake-gpu-operator), the model servers (vLLM simulator, so token
counts and latencies are modelled), and the BlueField DPU (a separate node joined to the GPU node by a
tunnel). This lab does not show GPU memory isolation, DPU offload or host-CPU savings.

# What changes with a real BlueField-3

On BlueField-3, TMM runs on the DPU itself rather than on a server core. The intent is that ingress,
rate limiting and metering stop competing with the inference server for host CPU, and refused
requests never touch the host at all. That is the design; it is not measured here. Figures for it
come from F5's BlueField-3 testing, not from this lab.

Nothing in the integration changes when that happens. The controller writes Kubernetes objects; where
TMM runs is BNK's concern.

# The open-source pieces

- **Tenancy controller and token meter**: Apache 2.0, in the **Lab code** tab under `controller/`. It
  watches `kai.resources/v1alpha1` Projects and writes standard Gateway API and BNK objects.
- **Upstream**: a docs pull request and an issue to `dsx-ai-factory/run-ai` are planned, describing
  how a service plane can consume Run:ai's tenant model. Not yet filed.

# Optional stretch

The same objects are a natural surface for an MCP server: an agent could ask "which project used the
most tokens per GPU this week" against Prometheus and the Run:ai Project annotations. Not part of
this lab.

Click **Check** to finish.
