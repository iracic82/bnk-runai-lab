---
slug: meet-the-factory
id: ""
type: challenge
title: Meet the AI factory
teaser: A real Kubernetes cluster with BNK 2.4 and open-source NVIDIA Run:ai. The GPUs are simulated.
notes:
- type: text
  contents: |-
    Four nodes. **control** runs Kubernetes and your terminal. **dpu** stands in for a BlueField DPU
    and runs BNK's data plane. **gpu** has four simulated A10 GPUs where Run:ai places models.
    **runai** runs the Run:ai control plane.

    Every request will enter through BNK on the dpu node and cross a tunnel to the gpu node, the same
    path it takes on real hardware.
tabs:
- id: ""
  title: Terminal
  type: terminal
  hostname: control
difficulty: basic
timelimit: 1800
---
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
