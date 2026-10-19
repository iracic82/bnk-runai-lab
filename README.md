# BNK 2.4 with open-source NVIDIA Run:ai

**You define a tenant in Run:ai. F5 BIG-IP Next for Kubernetes (BNK) builds its front door, rate
limit and token meter for you.**

This is the source of an Instruqt lab I built for GTC Berlin. You get four VMs and one Kubernetes
cluster running BNK 2.4 and the open-source NVIDIA Run:ai platform, with a few AI tenants sharing the
same GPUs. The GPUs are fake. The rest is real software: the parts that decide whether a request gets
in, how fast, and which worker serves it.

A new Run:ai project has a working BNK front door about 30 seconds later. Nobody opens a BNK config.

### ▶ [Open the interactive lab topology](https://iracic82.github.io/bnk-runai-lab/platform/topology/topology.html)

The same page learners have in the lab's Topology tab. Pick a flow and watch the packets travel
across the four nodes, step by step: define a tenant, chat, over its share, cross-tenant route,
Dynamo, token metering, AI agent, delete. Click any box to see what it is and its real address.

**Contents:** [The idea](#the-idea-in-one-picture) · [What the lab shows](#what-the-lab-shows) ·
[A request](#what-a-request-goes-through) · [The cluster](#the-cluster) · [Challenges](#the-challenges) ·
[Dynamo](#dynamo-with-bnk-picking-the-worker) · [AI agent](#an-ai-agent-that-runs-it) ·
[Real and simulated](#real-and-simulated) · [Repo map](#what-is-in-this-repo)

> The architecture view, BNK inside NVIDIA's Run:ai stack, is
> [live here](https://iracic82.github.io/bnk-runai-lab/docs/visuals/lab-topology.html).

> **Tip:** every diagram below is live Mermaid. Hover it on GitHub to get zoom, pan and a full-screen
> button.

## The idea in one picture

Run:ai owns the GPU. BNK owns the request. Today a platform team has to describe every tenant twice,
once on each side, and keep the two in sync by hand. The tenancy controller in this repo watches Run:ai
and writes the BNK side, so there is only one place to get it right.

```mermaid
flowchart LR
    admin(["👤 Platform admin<br/>Run:ai UI · API · labctl"])
    agent(["🤖 AI agent<br/>any LLM over MCP"])
    client(["💻 Tenant app<br/>any OpenAI SDK"])

    subgraph RUNAI["NVIDIA Run:ai · owns the GPU"]
        proj["Project tenant-a<br/>GPU quota · priority · department"]
        sched["Scheduler<br/>GPU fractions"]
    end

    subgraph CTRL["Tenancy controller · open source, this repo"]
        rec["Reconciler<br/>Project → BNK objects"]
        meter["Token meter<br/>Prometheus · ledger · Run:ai events"]
    end

    subgraph BNK["F5 BNK 2.4 · owns the request"]
        gw["Gateway + VIP<br/>one per project"]
        rl["Rate limit<br/>20 req/s × GPU quota"]
        epp["Endpoint Picker<br/>vLLM or Dynamo mode"]
    end

    subgraph GPU["GPU node · simulated A10s"]
        pool["Model replicas<br/>vLLM twin · Dynamo Mocker"]
    end

    admin -->|"define once"| proj
    agent -->|"Run:ai MCP · human approves"| proj
    proj -.->|watch| rec
    rec -.->|create| gw & rl & epp
    sched -.->|place| pool
    client ==> gw ==> rl ==> epp ==> pool
    gw -.->|tokens| meter
    meter -.->|"TokensMetered event"| proj

    classDef runai fill:#eaf5d9,stroke:#76b900,color:#1d2a0a
    classDef ctrl fill:#fff6dc,stroke:#d4a017,color:#3d2e00
    classDef bnk fill:#fde8ea,stroke:#e4002b,color:#4a0010
    classDef gpu fill:#ececec,stroke:#555,color:#111
    classDef actor fill:#e6f3fb,stroke:#2b8ac6,color:#0b2a40
    class proj,sched runai
    class rec,meter ctrl
    class gw,rl,epp bnk
    class pool gpu
    class admin,agent,client actor
```

Thick lines are inference traffic. Dashed lines are configuration.

What the controller builds from one Run:ai project:

| From Run:ai | Becomes in BNK |
|---|---|
| Project `tenant-a` | Its own Gateway and VIP, an HTTPRoute, an InferencePool and an Endpoint Picker |
| GPU quota (`gpuDeserved`) | A rate limit at the edge: `20 req/s × max(GPU quota, 0.25)` |
| Queue priority | The inference objective's criticality (100 and above is Critical) |
| Namespace marked as running Dynamo | The Endpoint Picker in Dynamo mode, listening to Dynamo's KV-cache events |
| Project deleted | Every BNK object for it removed |

## What the lab shows

These are the eight flows on the topology page. Every one runs live in the lab, and each has
something on screen that proves it.

| Flow | What happens | What proves it |
|---|---|---|
| **Define once** | Create a Run:ai project. Its BNK Gateway, route, picker and limit follow on their own | Project Ready → Gateway Programmed in about 30 s |
| **Isolation** | A tenant over its GPU share gets a 429 at the BNK edge, before any GPU | `tenant-b` 74% of its excess refused, `tenant-a` 0% |
| **Metering** | Tokens counted at BNK from every answer, booked to the Run:ai project and its department | Department total equals the sum of its projects |
| **Workload map** | Run:ai places each model on a GPU fraction. BNK's picker chooses the replica | Grafana shows both planes for one tenant |
| **NVIDIA Dynamo** | BNK reads Dynamo's KV-cache events and picks the worker | Each prompt on one worker, 6 out of 6 |
| **Tenant boundary** | A route can't attach to another tenant's Gateway | `Accepted=False`, `NotAllowedByListeners` |
| **Lifecycle** | Delete the project and BNK forgets the tenant | Every BNK object gone, Run:ai never written to |
| **AI agent** | An agent asks for a tenant over MCP, a person approves, BNK builds it | Dry run → approve → VIP Programmed at 20 req/s |

## What a request goes through

```mermaid
sequenceDiagram
    autonumber
    participant C as Tenant app
    participant T as BNK TMM (DPU)
    participant E as Endpoint Picker
    participant M as Model replica (GPU node)
    participant P as Token meter

    C->>T: POST /v1/chat/completions to the tenant VIP
    alt over the tenant's share
        T-->>C: 429 tenant/rate-limited (no GPU touched)
    else within quota
        T->>E: which replica?
        E-->>T: the one that already holds this prompt's KV cache
        T->>M: request
        M-->>T: answer
        T-->>C: answer + X-BNK-Tenant header
        T--)P: tokens in and out, per project
    end
```

The 429 comes from BNK, so an over-quota request never reaches a GPU. In challenge 3 BNK refused
74% of `tenant-b`'s excess traffic while `tenant-a`, on the same GPU at the same moment, had nothing
refused.

## The cluster

On real hardware, BNK's data plane (TMM) runs on a BlueField-3 DPU inside the GPU server. Here the DPU
is its own VM, joined to the GPU node by a GRE tunnel that plays the part of the PCIe link.

```mermaid
flowchart LR
    subgraph control["🖥️ control · n2-standard-16 · the glue"]
        k8s["Kubernetes 1.30<br/>control plane"]
        tc["Tenancy controller<br/>+ token meter :9102"]
        obs["Prometheus + Grafana :3000"]
        mcp["MCP servers<br/>Run:ai MCP :8080<br/>bnk-tenancy-mcp :8090"]
    end
    subgraph runai["🟩 runai · n2-standard-32"]
        rcp["Run:ai control plane<br/>UI · API · Keycloak · Postgres<br/>about 50 services"]
    end
    subgraph dpu["🟥 dpu · n2-standard-8 · simulated BlueField-3"]
        bc["BNK controller<br/>Gateway API → TMM"]
        tmm["BNK TMM<br/>one VIP per project<br/>172.16.201.110-149"]
        pick["Endpoint Pickers<br/>vLLM mode · Dynamo mode"]
    end
    subgraph gpu["⬛ gpu · n2-standard-16 · 4 × A10 simulated"]
        sched["Run:ai scheduler<br/>GPU fractions"]
        models["vLLM twin replicas"]
        dyn["Dynamo Mocker workers<br/>+ NATS"]
    end
    rcp -.-> tc
    tc -.-> bc --> tmm
    tmm <==>|"GRE tunnel = PCIe link<br/>internal-net 10.1.20.0/24"| models & dyn
    pick --- tmm
    dyn -.->|KV events| pick

    classDef c fill:#fff6dc,stroke:#d4a017,color:#3d2e00
    classDef r fill:#eaf5d9,stroke:#76b900,color:#1d2a0a
    classDef d fill:#fde8ea,stroke:#e4002b,color:#4a0010
    classDef g fill:#ececec,stroke:#555,color:#111
    class k8s,tc,obs,mcp c
    class rcp,sched r
    class bc,tmm,pick d
    class models,dyn g
```
The interactive version animates each of the eight flows above:
**[open the packet-flow topology](https://iracic82.github.io/bnk-runai-lab/platform/topology/topology.html)** (source:
[`platform/topology/topology.html`](platform/topology/topology.html), one self-contained file).

## The challenges

| # | Challenge | What you see |
|---|---|---|
| 1 | [Meet the AI factory](instruqt/bnk-runai/01-meet-the-factory/assignment.md) | The cluster, Run:ai, BNK, and what is real versus simulated |
| 2 | [Define a tenant once](instruqt/bnk-runai/02-tenant-defined-once/assignment.md) | Create a Run:ai project, watch its BNK front door appear |
| 3 | [Isolation before the GPU](instruqt/bnk-runai/03-isolation-before-the-gpu/assignment.md) | One tenant overloads, gets 429 at the edge, the other keeps its latency |
| 4 | [Tokens per project](instruqt/bnk-runai/04-tokens-per-project/assignment.md) | Tokens counted at the edge, per project and department |
| 5 | [NVIDIA Dynamo, with BNK picking the worker](instruqt/bnk-runai/05-dynamo-on-runai/assignment.md) | BNK reads Dynamo's KV-cache events and routes each prompt to the worker that holds it |
| 6 | [What you built, and what's next](instruqt/bnk-runai/06-debrief/assignment.md) | An AI agent creates a tenant over MCP, a human approves |

<table>
<tr>
<td><img src="instruqt/bnk-runai/assets/02-tenant-flow.png" alt="Tenant defined once"></td>
<td><img src="instruqt/bnk-runai/assets/03-isolation.png" alt="Isolation before the GPU"></td>
</tr>
<tr>
<td><img src="instruqt/bnk-runai/assets/04-metering.png" alt="Token metering"></td>
<td><img src="instruqt/bnk-runai/assets/05-dynamo-flow.png" alt="Dynamo with BNK picking the worker"></td>
</tr>
</table>

## Dynamo, with BNK picking the worker

This is the part people ask about most. NVIDIA Dynamo runs as just another Run:ai project. Each
worker announces on NATS every KV-cache block it stores or evicts. BNK 2.4's Endpoint Picker has a
Dynamo mode that listens to those announcements, works out which worker already holds the prompt, and
sends the request there. Dynamo's own router isn't used.

```mermaid
flowchart LR
    c(["💻 Client"]) ==> vip["BNK VIP<br/>rate limit · metering"]
    vip ==> epp["BNK Endpoint Picker<br/>engine: dynamo"]
    epp ==>|"request names worker 1"| w1["Worker 1<br/>Mocker + direct-mode frontend<br/>0.5 GPU"]
    epp -.->|"or worker 2"| w2["Worker 2<br/>Mocker + direct-mode frontend<br/>0.5 GPU"]
    w1 & w2 -->|"KV-cache stored / evicted"| nats[("NATS<br/>Dynamo tenants only")]
    nats -->|subscribe| epp

    classDef bnk fill:#fde8ea,stroke:#e4002b,color:#4a0010
    classDef dyn fill:#f1e8fd,stroke:#8b4fd8,color:#24104a
    class vip,epp bnk
    class w1,w2,nats dyn
```
In the lab you send two long prompts six times each. Each one lands on the same worker all six
times. NATS has no authentication, so a NetworkPolicy keeps everyone but Dynamo tenants off it. Without
that, any pod could forge cache events and steer the picker. The notes are in
[`docs/dynamo-research.md`](docs/dynamo-research.md).

## An AI agent that runs it

In challenge 6 you ask an agent for a tenant in plain English. It's about 250 lines of plain Python with no
framework. It talks to NVIDIA's Run:ai MCP server, to our read-only `bnk-tenancy-mcp`, and to any
OpenAI-compatible model. The lab uses Nemotron on build.nvidia.com.

```mermaid
sequenceDiagram
    participant U as You
    participant A as agent.py (Nemotron)
    participant R as Run:ai MCP server (NVIDIA)
    participant B as bnk-tenancy-mcp (read-only)
    U->>A: "Give agent-demo 1 GPU in the default department"
    A->>R: list_departments, manage_org_unit (dry run)
    A->>U: proposed change, approve?
    U->>A: yes
    A->>R: manage_org_unit (create)
    Note over R,B: tenancy controller builds the BNK front door
    A->>B: get_tenant_front_door agent-demo
    B-->>A: VIP, rate limit, status
    A->>U: agent-demo is live at its VIP
```

A person approves every change. The gate fails closed: a tool counts as a write unless it's on a
short read-only list, so a new or renamed tool can't slip through. Code in
[`platform/mcp/agent.py`](platform/mcp/agent.py), design notes in [`docs/mcp.md`](docs/mcp.md).

## Real and simulated

| Real | Simulated |
|---|---|
| BNK 2.4 data and control plane, rate limits, Endpoint Picker, metering path | The GPUs (fake-gpu-operator, 4 × A10) |
| Run:ai control plane, projects, quotas, scheduler, GPU fractions | Model servers: vLLM twin and Dynamo Mocker (real tokenizer, no weights) |
| Kubernetes, the tenancy controller, Prometheus and Grafana | The BlueField DPU (a separate node with a tunnel) |
| Dynamo operator, frontend and its KV-cache events over NATS | |
| Both MCP servers and the LLM the agent talks to | |

## What is in this repo

| Path | What it is |
|---|---|
| [`controller/`](controller) | The tenancy controller, token meter and `bnk-tenancy-mcp`, in Go (`cmd/manager`, `cmd/meter`, `cmd/mcp`) |
| [`charts/bnk-runai-tenancy`](charts/bnk-runai-tenancy) | Helm chart for the controller and meter |
| [`platform/`](platform) | Install scripts: cluster and BNK, Run:ai, fake GPUs, models, Dynamo, MCP, observability, topology page |
| [`labctl/`](labctl) | The learner CLI: `labctl tenant create`, `labctl load overload`, `labctl chat` and more |
| [`loadgen/`](loadgen) | Load generator for steady, overload and prefix-heavy traffic |
| [`instruqt/bnk-runai/`](instruqt/bnk-runai) | The Instruqt track: challenges, setup scripts, slides |
| [`platform/versions.env`](platform/versions.env) | Every pinned version and image digest |
| [`docs/`](docs) | Design notes: Dynamo, MCP, verified BNK resource names, build and deploy flow |

## Try the CLI

On the control node inside the lab:

```bash
labctl tenant create tenant-a --gpu 1 --priority High
labctl model deploy tenant-a
labctl chat tenant-a "Hello from the BNK front door"
labctl load overload --tenant tenant-b --seconds 30
labctl tenant show tenant-a
```

## Versions

BNK 2.4.0 on Kubernetes 1.30, open-source Run:ai from
[dsx-ai-factory/run-ai](https://github.com/dsx-ai-factory/run-ai), NVIDIA Dynamo operator 1.5.1,
fake-gpu-operator 0.2.1, kube-prometheus-stack 91.9.0, NVIDIA Run:ai MCP server 2.26.50. The full list,
with commits and image digests, is in [`platform/versions.env`](platform/versions.env).

## Further reading

- [NVIDIA and F5 on BlueField-3](https://blogs.nvidia.com/blog/nvidia-f5/)
- [Open-source NVIDIA Run:ai](https://github.com/dsx-ai-factory/run-ai)
- [NVIDIA Dynamo](https://github.com/ai-dynamo/dynamo)

## License

Apache 2.0, see [LICENSE](LICENSE).
