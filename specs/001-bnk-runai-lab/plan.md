# Implementation Plan: BNK 2.4 × Open-Source Run:ai — "One Tenant, Two Planes" Lab

**Branch**: `001-bnk-runai-lab` | **Date**: 2026-10-15 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-bnk-runai-lab/spec.md`

## Summary

A new Instruqt track, plus a GTC Berlin demo from the same code. It shows that a tenant
defined once in Run:ai (a Run:ai Project) gets a full BNK 2.4 data-path
contract automatically: front door, rate limit from GPU quota, model-aware routing to
the replicas the Run:ai Scheduler placed, priority enforcement aligned with Run:ai preemption, token
metering, safe rollout, and egress identity. Everything runs as real software; only
GPUs and model inference are simulated (fake-gpu-operator, llm-d-inference-sim). The
glue is a small Apache 2.0 **tenancy controller** (Go, controller-runtime), which is
also the artifact NVIDIA needs to list F5.

## Technical Context

**Language/Version**: Go 1.24 (tenancy controller, metering exporter), Bash
(labctl, Instruqt scripts), Python 3.12 (load generator, check scripts)
**Primary Dependencies**: BNK 2.4.0 (bnk-deploy installer + bnk-ai-kv-lab node prep, reused); **open-source
NVIDIA Run:ai** (control plane + `runai-chart`, which bundles the Run:ai Scheduler/KAI and KRM),
images built from `dsx-ai-factory/run-ai` and pushed to our GAR; fake-gpu-operator;
llm-d-inference-sim (or the vllm-twin sim from bnk-ai-kv-lab, which speaks vLLM KV events to F5EPP);
Gateway API + GAIE `inference.networking.k8s.io/v1`; kube-prometheus-stack + Grafana; controller-runtime
**Storage**: N/A (Kubernetes API is the state store)
**Testing**: Go unit + envtest for the controller; challenge check scripts as
acceptance tests; `instruqt track test`
**Target Platform**: Instruqt, real kubeadm cluster on image `f5/bnk-f5`:
`bnk` (n2-standard-16, control plane), `dpu` (n2-standard-8, TMM / simulated BlueField),
`server` (n2-standard-16, fake GPU pool + model pods), `runai` (n2-standard-16 / 64 GB,
Run:ai control plane). K8s 1.30, Calico, BNK 2.4.0 via bnk-deploy
**Project Type**: Kubernetes operator + lab content (Instruqt track) + demo script
**Performance Goals**: Project → serving tenant ≤30 s; lab start ≤5 min; load runs
≈30 s; SC-002/003 visible on dashboards
**Constraints**: no real GPUs; no secrets in images (team secrets FAR_PULL_B64,
BNK_LICENSE_JWT, GH_READ_TOKEN); linux/amd64 images on Docker Hub or GAR; tenant namespaces
must be in BNK_EPP_NAMESPACES at install time; GTC demo recorded by 2026-10-19
**Scale/Scope**: 2 tenants (prod-chat, research) + 1 department; 3 fake GPU nodes ×
2–4 fake A10; 8 challenges (7 core + 1 stretch); one controller (~1.5k LOC)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Evidence |
|---|---|---|
| I Real software, simulated hardware | ✅ | BNK 2.4 real, full Run:ai real; only GPUs/inference simulated (R2, R3, R5) |
| II Verified names only | ⚠️ partial | Run:ai/KAI names verified (`docs/verified-names.md`); BNK 2.4 names gated by spike S0 |
| III Tenant defined once | ✅ | Controller derives the whole BNK tenant bundle from Project+Queue (data-model) |
| IV Every feature earns its place | ✅ | R6 mapping table: each BNK feature tied to a Run:ai trigger |
| V Instruqt-ready | ✅ | Prebaked image, labctl, check/solve per challenge, secrets via Instruqt (R8, R9) |
| VI Honest claims | ✅ | DPU/host-CPU/Workload Map/GPU isolation marked architecture-only; metering fallback disclosed (R7) |

**Gate result**: PASS with one open item. II becomes fully green only when spike S0
verifies BNK 2.4 CR names. No code may use an unverified BNK name before that.

## Phases & Gates

| Phase | Output | Gate to exit |
|---|---|---|
| **S0 Spike** (Oct 15–16) | A sandbox of the new track: reused real BNK 2.4 cluster + `runai` VM + full Run:ai + fake-gpu + sim; projects created in the Run:ai UI; hand-applied BNK tenant bundle | ✅ BNK names verified · ✅ license works in a VM · ✅ EPP routes to sim pods · ✅ VM headroom measured · ✅ token-count answer |
| **P1 Controller MVP** (Oct 16–17) | Project/Queue → GatewaySettings/route/InferencePool/rate limit | US1 acceptance passes on spike VM |
| **P2 GTC demo** (Oct 17–19) | US2–US4 scenarios, Grafana board, 8-min script, recording | SC-007 |
| **P3 Instruqt image + track** (after GTC) | Packer image, labctl, challenges C1–C7 | SC-005, SC-006 |
| **P4 Stretch + publish** | C8 P/D, public controller repo, Helm chart, validation matrix | Brief W3 packet |

## Track Outline (the "appealing" part)

| # | Challenge | Run:ai moment | BNK 2.4 shines by… | Wow moment on screen |
|---|---|---|---|---|
| C1 | Meet the AI Factory | Run:ai UI + fake GPUs, department `acme` | BNK installed and idle | `kubectl get nodes` shows GPUs that cost nothing |
| C2 | One tenant, defined once | Create Projects `prod-chat` (2 GPU) and `research` (0.5 GPU) | Controller creates listeners, routes, rate limits | BNK objects appear, labelled with Run:ai project. Delete one, and they vanish |
| C3 | Fractions behind one door | Run:ai packs replicas onto GPU fractions | InferencePool + Endpoint Picker, KV-prefix affinity | Side-by-side TTFT: round-robin vs EPP |
| C4 | Same priority, both planes | Run:ai preempts research under contention | InferenceObjective sheds research, protects prod | prod p95 flat while research degrades gracefully |
| C5 | Who used the tokens? | Queue allocation per project | Per-project metering | Tokens per project next to GPU per project |
| C6 | Ship v2 without fear | Run:ai deploys `chat-v2` | InferenceModelRewrite 90/10 → 0/100 | Clients unchanged, zero errors |
| C7 | Every tenant, its own exit | Project namespace pulls models | EgressGateway per-namespace SNAT | Model hub log shows one IP per tenant |
| C8* | Disaggregated serving | Run:ai gang-schedules prefill + decode | P/D-aware routing | Prefill and decode served by different pools |

Debrief: architecture slide with the request path. DPU/BlueField-3 and host-CPU
savings are shown as **architecture + recorded GPU-cluster demo**, not as a live
result.

## Project Structure

### Documentation (this feature)

```text
specs/001-bnk-runai-lab/
├── plan.md              # This file
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1 validation guide
├── contracts/
│   ├── tenancy-controller.md   # Watched inputs → owned outputs, annotations
│   ├── metrics.md              # bnk_tokens_total + fallback contract
│   └── labctl.md               # CLI commands used by learners and checks
└── tasks.md             # /speckit-tasks
```

### Source Code (repository root)

```text
controller/                 # Go, controller-runtime, Apache 2.0
├── cmd/manager/
├── internal/reconcile/     # project_controller.go, queue mapping
├── internal/bnk/           # BNK object builders (names from verified-names.md)
├── config/                 # RBAC, deployment, CRD-less (watches upstream CRDs)
└── test/envtest/

metering/                   # exporter: bnk_tokens_total / fallback
charts/bnk-runai-tenancy/   # Helm chart (controller + exporter + dashboards)
platform/                   # install scripts + pinned versions for the whole stack
├── versions.env
├── cluster/                # wrapper over bnk-ai-kv-lab node-prep + bnk-deploy (reused, pinned refs)
├── bnk/                    # tenant-independent BNK objects (GatewayClass, shared Gateway, VLANs)
├── runai/                  # Run:ai control plane + runai-chart values, image list (GAR), department/projects via Run:ai API
├── fake-gpu/
├── models/                 # llm-d-inference-sim deployments per tenant
└── observability/          # Prometheus, Grafana dashboards
labctl/                     # learner CLI (bash)
loadgen/                    # prefix-heavy and overload traffic profiles
instruqt/                   # track.yml, challenges/NN-*/{assignment,setup,check,solve}
demo/gtc/                   # 8-minute script, recording notes
docs/verified-names.md
```

**Structure Decision**: a single repo holding the operator, platform install, and lab
content, so the controller, chart, and track version together (one validation matrix).

## Complexity Tracking

| Choice | Why needed | Simpler alternative rejected because |
|---|---|---|
| Custom controller instead of scripts | "Defined once" must be live (create, quota change, delete, drift) and publishable as open code for the NVIDIA listing | Scripts can't show reconciliation and aren't a credible partner artifact |
| Real 3–4 VM kubeadm cluster (not Kind) | BNK data plane (TMM, hugepages, Multus) does not run fully on Kind; this cluster is proven on Instruqt | Kind: no data plane; a new cluster design repeats solved problems |
| Two simulators (fake-gpu + llm-d-sim) | GPU placement and inference behaviour are different layers | A single fake can't make Run:ai schedule *and* EPP route on real metrics |
