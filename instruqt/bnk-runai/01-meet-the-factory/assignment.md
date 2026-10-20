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
- id: aj7gog5kmtw7
  title: Topology
  type: service
  hostname: control
  path: /topology.html
  port: 3002
- id: ihgbtoqirsea
  title: Terminal 2
  type: terminal
  hostname: control
- id: rxcn8zg0rf4u
  title: Run:ai UI
  type: service
  hostname: control
  port: 3001
- id: qsve6plpjcfh
  title: Grafana
  type: service
  hostname: control
  path: /d/one-tenant-two-planes?orgId=1&kiosk&hideLogo&refresh=5s
  port: 3000
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
# Meet the AI factory

This lab joins two products that normally never talk to each other:

- **NVIDIA Run:ai** decides who gets which GPUs.
- **F5 BIG-IP Next for Kubernetes (BNK)** decides which requests reach those GPUs.

A small controller links them, so a tenant defined once in Run:ai also exists in BNK.

![BNK in the Run:ai architecture](../assets/01-architecture.png)

NVIDIA's Run:ai layers are green and grey, F5 is red, and the controller that joins them is yellow.
Each layer shows the node it runs on.

---

## Your tabs

| Tab | What it is |
|---|---|
| **Terminal** | `kubectl` and `labctl` on the control node |
| **Topology** | the whole lab as an animated diagram. Pick a flow and watch it travel |
| **Run:ai UI** | the open-source Run:ai console, in a browser inside the lab |
| **Grafana** | "One tenant, two planes": Run:ai's GPU view next to BNK's request view |
| **Lab code** | the controller, the installers and these challenges |
| **GPU node / DPU node** | shells on those machines, if you want to look around |

Every command block has a **run** button, which runs it in the Terminal tab for you.

---

## Step 1 · Four real nodes

```bash,run
kubectl get nodes -L app,run.ai/simulated-gpu-node-pool
```

| Node | Role |
|---|---|
| `control` | Kubernetes control plane, the tenancy controller, Grafana |
| `dpu` | stands in for a BlueField DPU. BNK's data plane, **TMM**, runs here |
| `gpu` | four simulated NVIDIA A10s. Model servers run here |
| `runai` | the Run:ai control plane: about 30 services, Keycloak, Postgres, NATS |

The GPUs come from NVIDIA's own GPU simulator, so the scheduler sees real GPU resources:

```bash,run
kubectl get node gpu -o jsonpath='{.status.allocatable.nvidia\.com/gpu} GPUs on node gpu{"\n"}'
```

**You should see:** `4 GPUs on node gpu`.

---

## Step 2 · BNK 2.4

TMM runs on the `dpu` node. The BNK controller turns Kubernetes objects into TMM configuration.

```bash,run
kubectl get pods -n f5-bnk -o wide | grep -E 'NAME|tmm|cne-controller'
kubectl get gatewayclass
```

**You should see:** `f5-gateway-class` with `ACCEPTED True`.

That class is how a Kubernetes Gateway asks for BNK. Every tenant front door in this lab is an ordinary
Gateway API object, and BNK serves it.

---

## Step 3 · Open-source Run:ai

Run:ai comes in two halves. The control plane holds the UI, API, identity and database. The cluster
side holds the scheduler, operators and agent.

```bash,run
kubectl get pods -n runai-backend --no-headers | awk '{print $3}' | sort | uniq -c
kubectl get pods -n runai | grep -E 'NAME|scheduler|operator|agent|project'
```

Two names will come up again: the scheduler is called `runai-scheduler`, and a workload joins a
project's queue through the label `runai/queue`.

Now open the **Run:ai UI** tab and sign in:

| User | Password |
|---|---|
| `admin@acme.lab` | `Lab-Admin-1!` |

Have a look under **Resources → Clusters**, where you'll find one cluster called `acme-ai-factory`, and
under **Resources → Nodes**, where the `gpu` node shows its 4 GPUs.

There are no projects yet. You'll create them in the next challenge, in Run:ai, and BNK will follow on
its own.

---

## Step 4 · The glue

The tenancy controller is the only new piece. It watches Run:ai projects and nothing else.

```bash,run
kubectl -n bnk-runai-tenancy get pods
kubectl -n bnk-runai-tenancy logs deploy/bnk-runai-tenancy --tail=5
```

**You should see:** two pods Running, the **controller** and the **token meter**.

Open the **Grafana** tab too. The board is empty for now. Each tenant you create gets its own row there.

---

> **What is real:** BNK 2.4, open-source Run:ai and its scheduler, Kubernetes, every network path.
>
> **What is simulated:** the GPUs (NVIDIA fake-gpu-operator), the model servers (a vLLM simulator),
> and the DPU (a VM with a tunnel to the GPU node: the path is real, the offload is not).

Click **Check** when you've looked around.
