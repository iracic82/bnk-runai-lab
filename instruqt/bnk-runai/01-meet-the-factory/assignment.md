---
slug: meet-the-factory
id: pwzmtvaqmzpb
type: challenge
title: Meet the AI factory
teaser: A real Kubernetes cluster with BNK 2.4 and open-source NVIDIA Run:ai. The
  GPUs are simulated.
notes:
- type: image
  url: ../assets/notes-01-hero.png
- type: image
  url: ../assets/notes-01-real.png
tabs:
- id: yu2uqgpy76yf
  title: Terminal
  type: terminal
  hostname: control
- id: muyniiuokbjw
  title: Lab code
  type: code
  hostname: control
  path: /root/lab
- id: hkgsqpgyzxeq
  title: GPU node
  type: terminal
  hostname: gpu
- id: mx8qyln7lpej
  title: DPU node
  type: terminal
  hostname: dpu
difficulty: basic
timelimit: 1800
enhanced_loading: null
---
Here is the stack you are about to explore. NVIDIA's Run:ai layers are in green and grey, the F5
layer in red, and the controller that joins them in yellow. Each layer shows the node it runs on.

![BNK in the Run:ai architecture](../assets/01-architecture.png)

The **Lab code** tab shows the controller and install scripts you are running. The **GPU node** and
**DPU node** tabs are shells on those machines, if you want to look around.

# The cluster

```bash
kubectl get nodes -L app,run.ai/simulated-gpu-node-pool
```

The gpu node reports GPUs that cost nothing:

```bash
kubectl get node gpu -o jsonpath='{.status.allocatable.nvidia\.com/gpu}{"\n"}'
```

# BNK 2.4

TMM, BNK's data plane, runs on the dpu node:

```bash
kubectl get pods -n f5-bnk -o wide | grep -E 'tmm|controller'
```

# Run:ai

The control plane and the cluster services:

```bash
kubectl get pods -n runai-backend --no-headers | wc -l
kubectl get pods -n runai
```

Click **Check** when you've looked around.
