---
description: "Task list for BNK 2.4 × Open-Source Run:ai lab"
---

# Tasks: BNK 2.4 × Open-Source Run:ai — "One Tenant, Two Planes" Lab

**Input**: Design documents from `/specs/001-bnk-runai-lab/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: The plan requires Go unit and envtest tests for the controller, plus
check scripts per challenge (constitution quality gates). They are included.

**Organization**: Phase 0 is the spike (hard gate). Then Setup, Foundational, one phase
per user story, and Polish/Publish.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on incomplete tasks)
- **[Story]**: user story from spec.md (US1–US7)
- **Gate tags** (end of each description):
  - 🟢 **pre-spike**: can be built now, with no BNK dependency
  - 🔒 **BNK-gated**: blocked until T008 records verified BNK 2.4 names in
    `docs/verified-names.md` (Constitution Principle II)

## Path Conventions (from plan.md)

`controller/` (Go operator) · `metering/` · `charts/bnk-runai-tenancy/` · `platform/` ·
`labctl/` · `loadgen/` · `instruqt/` · `demo/gtc/` · `docs/`

---

## Phase 0: S0 Spike — HARD GATE (Oct 15–16)

**Purpose**: Prove that full Run:ai installs on top of the proven real 3-VM BNK 2.4 cluster
(bnk-ai-kv-lab / Lambda POC), and verify every BNK name we need beyond those already proven.

**⚠️ Nothing tagged 🔒 starts until T011 (gate review) passes.**

- [ ] T001 Create the new Instruqt track skeleton `instruqt/bnk-runai/config.yml`, copying the VM layout and secrets of `bnk-ai-kv-lab/instruqt/bnk-ai-demos-kv/config.yml` (image `f5/bnk-f5`; VMs bnk n2-standard-16, dpu n2-standard-8, server n2-standard-16; secrets FAR_PULL_B64, BNK_LICENSE_JWT, GH_READ_TOKEN), plus a minimal `track.yml` and a spike-only challenge, and record decisions in `docs/spike/S0-report.md`
- [ ] T002 Write `instruqt/bnk-runai/track_scripts/setup-bnk`, reusing the bnk-ai-kv-lab node prep + bnk-deploy install unchanged (pinned refs). Set `BNK_EPP_NAMESPACES=kai-prod-chat,kai-research` before install (tenant namespaces must be known at install time, research R11), and write it to `platform/cluster/setup.sh`
- [ ] T003 Push the spike track to Instruqt as private/unpublished (`instruqt track push`), start a sandbox, and record cluster bring-up time and per-node allocatable CPU/RAM after BNK in `docs/spike/S0-report.md`
- [ ] T004 Confirm license activation and a Ready TMM on the dpu node (reuse `ensure_tmm_external` logic), and record it in `docs/spike/S0-report.md`
- [ ] T005 Build the open-source Run:ai images once from `dsx-ai-factory/run-ai` (`local-dev.sh` build phase, linux/amd64) and push them to our GAR. Add a `runai` VM (n2-standard-16/64 GB) to `instruqt/bnk-runai/config.yml`. Install the **Run:ai control plane + `runai-chart`** (which brings the Run:ai Scheduler/KAI and KRM) and fake-gpu-operator (server node labelled `run.ai/simulated-gpu-node-pool=default`, 4 × A10) via `platform/runai/install.sh` + `platform/fake-gpu/install.sh`. Confirm everything runs on K8s 1.30, and record image digests in `platform/versions.env`
- [ ] T006 In the **Run:ai UI**, create Department `acme` and Projects `prod-chat` (2 GPU quota, high priority) and `research` (0.5 GPU, low priority). Record which namespaces Run:ai creates (`runai-<project>` or `kai-<project>`; must match `BNK_EPP_NAMESPACES` in T002) and the resulting cluster objects (Project CRD, Queue) the controller will watch, in `docs/spike/S0-report.md`
- [ ] T007 Deploy the model sim (llm-d-inference-sim or bnk-ai-kv-lab `sims/vllm-twin` image) as a **Run:ai inference workload** in each project (Run:ai UI/CLI, 0.5 GPU fraction). Record what Run:ai renders (Knative Service vs plain Deployment, pod labels). Confirm the Run:ai Scheduler packs both onto one fake GPU and that F5EPP-compatible `vllm:*` metrics and KV events are exposed, and record in `docs/spike/S0-report.md`
- [ ] T008 By hand, apply a tenant bundle for `prod-chat` using the proven kinds (GatewayClass `f5.com/f5-bnk-f5-cne-controller`, Gateway in `f5-bnk`, HTTPRoute in tenant ns, `F5EPP` `inference.k8s.f5.com/v1alpha1`, `InferencePool` `inference.networking.k8s.io/v1`, `NetPolicy` + `F5BigCneIrule` rate-limit iRule, `F5SPKEgress` v3 + `F5SPKSnatpool`). Also try `InferenceObjective` and `InferenceModelRewrite`. Record every verified kind/apiVersion/field in `docs/verified-names.md`
- [ ] T009 Answer and record in `docs/spike/S0-report.md`: (a) can the iRule count tokens per tenant, as the Lambda metering iRule does? (b) does F5EPP route correctly to Run:ai inference pods (including through Knative, if Run:ai renders one)? (c) are InferenceObjective/ModelRewrite present in BNK 2.4? (d) can a tenant namespace be added to the EPP namespaces after install without disruption?
- [ ] T010 Measure headroom with everything running. If the server node exceeds 80% CPU requests, add a 4th VM `gpu` (n2-standard-8) as the fake GPU pool in `instruqt/bnk-runai/config.yml`. Decide on the full Run:ai control plane (R5), and record in `docs/spike/S0-report.md`
- [ ] T011 Gate review: mark the BNK rows ✅ in `docs/verified-names.md`, and update research.md R5/R7/R11 with outcomes in `specs/001-bnk-runai-lab/research.md`

**Checkpoint**: Gate passed → 🔒 tasks unblocked. If Run:ai fails on 1.30, build a new cluster at a supported version on the team's GCP/AWS account (user-approved).

---

## Phase 1: Setup (can start in parallel with Phase 0)

- [ ] T012 Create the repo skeleton per plan.md (`controller/ metering/ charts/ platform/ labctl/ loadgen/ instruqt/ demo/gtc/ docs/`), plus `.gitignore` (excluding `*.jwt`, `*far*`, `.env`) and an Apache-2.0 `LICENSE` at repo root 🟢 pre-spike
- [ ] T013 [P] Initialize the Go module `github.com/f5devcentral/bnk-runai-tenancy` in `controller/go.mod` (Go 1.24, controller-runtime, client-go), with a `Makefile` providing `build test envtest image` targets 🟢 pre-spike
- [ ] T014 [P] Create `platform/versions.env` pinning the bnk-ai-kv-lab and bnk-deploy git refs, Run:ai image digests (GAR), fake-gpu-operator, llm-d-inference-sim, kube-prometheus-stack, Gateway API, and GAIE CRD versions (BNK versions left as `TODO-S0`) 🟢 pre-spike
- [ ] T015 [P] Add `scripts/secret-scan.sh` (gitleaks or a grep for JWT/FAR patterns) and wire it into a pre-commit hook in `.githooks/pre-commit` 🟢 pre-spike

---

## Phase 2: Foundational (blocking all user stories)

- [ ] T016 Write `platform/cluster/labels.sh` labelling the fake GPU node(s) `run.ai/simulated-gpu-node-pool=default` and tainting/keeping the dpu node for TMM only (Run:ai must not place model pods there) 🟢 pre-spike
- [ ] T017 [P] Write `platform/runai/install.sh`, installing the Run:ai control plane and `runai-chart` from our GAR images (pinned digests, fake-GPU sharing mode) and waiting for readiness 🟢 pre-spike
- [ ] T018 [P] Write `platform/fake-gpu/install.sh` and `platform/fake-gpu/values.yaml` (A10, 2 GPUs per worker, Prometheus wiring) 🟢 pre-spike
- [ ] T019 [P] Write `platform/runai/seed.sh`, creating Department `acme` through the Run:ai API (used by solve scripts and the GTC fallback; learners use the UI) 🟢 pre-spike
- [ ] T020 [P] Write `platform/models/sim-deployment.tmpl.yaml` for llm-d-inference-sim with labels `app.kubernetes.io/part-of=model-server`, `model=<name>`, `version=<v>`, annotation `gpu-fraction`, `schedulerName: kai-scheduler`, and latency profile values from the sim's `docs/latency-profiles.md` 🟢 pre-spike
- [ ] T021 [P] Write `platform/observability/install.sh` (kube-prometheus-stack with Grafana enabled, ServiceMonitors for the sim, fake-gpu DCGM, and the controller) 🟢 pre-spike
- [ ] T022 Write `platform/bnk/shared.yaml` with tenant-independent BNK objects (GatewayClass, shared Gateway `runai-gw` in `f5-bnk` with one HTTP listener per tenant hostname, VLANs reused from bnk-ai-kv-lab) 🔒 BNK-gated
- [ ] T023 Write `platform/install.sh` (runs after cluster setup.sh), orchestrating labels→Run:ai→fake-gpu→observability→shared BNK objects→controller. It must be idempotent and print a per-component status table 🔒 BNK-gated
- [ ] T024 [P] Write `labctl/labctl` (bash) with subcommand dispatch, `--json` output, cwd-independent paths (`LAB_HOME=/opt/lab`), and the `labctl status` implementation per `contracts/labctl.md` 🟢 pre-spike
- [ ] T025 [P] Write `loadgen/loadgen.py` (async OpenAI-compatible client; profiles `steady`, `prefix-heavy` (80% shared system prompt), `overload`, `rollout`; a `--seconds` default of 30; per-tenant Host header; prints p50/p95 TTFT and the status code histogram) 🟢 pre-spike
- [ ] T026 Scaffold the controller manager in `controller/cmd/manager/main.go` (scheme registration for unstructured `kai.resources/v1alpha1` Project/Department and `scheduling.run.ai` Queue, leader election, metrics on `:8080`, health probes) 🟢 pre-spike

**Checkpoint**: On the spike sandbox, `platform/install.sh` brings up Run:ai, fake GPU, and observability over BNK, and `labctl status` is all green.

---

## Phase 3: User Story 1 — Tenant defined once (P1) 🎯 MVP / GTC core

**Goal**: Create a Run:ai Project, and a BNK tenant bundle appears. Change the quota,
and the limit changes. Delete the Project, and the bundle is gone.

**Independent Test**: quickstart V1/V1b. The bundle appears within 30 s, is labelled
with the project, `curl` gets 200 via the tenant hostname, and deleting the Project
removes it within 30 s.

### Tests for US1

- [ ] T027 [P] [US1] Envtest that installs the Run:ai cluster Project/Department CRDs (`kai.resources`, as shipped in `runai-chart`) (copied to `controller/test/crds/`) and asserts that Project → bundle builder input mapping reads the namespace from `status.namespace`, never from the name, in `controller/test/envtest/project_mapping_test.go` 🟢 pre-spike
- [ ] T028 [P] [US1] Unit tests for the rate-limit derivation `rps = baseRps × max(deserved, minGpuFactor)`, the override annotation `tenancy.f5.com/rps`, and "Project without a GPU queue gets `rps = base_rps × 0.25` (never unlimited)", in `controller/internal/reconcile/ratelimit_test.go` 🟢 pre-spike
- [ ] T029 [P] [US1] Unit test for "Two Projects resolving to one namespace → Error state for both, no writes", in `controller/internal/reconcile/conflict_test.go` 🟢 pre-spike
- [ ] T030 [P] [US1] Envtest for finalizer lifecycle: the `tenancy.f5.com/bundle` finalizer is added on Ready, and on deletion the bundle is deleted before the finalizer is removed, in `controller/test/envtest/finalizer_test.go` 🟢 pre-spike

### Implementation for US1

- [ ] T031 [P] [US1] Implement the TenantSpec model (project, department, namespace, gpuDeserved, priority, overrides) plus parsing from unstructured Project/Department in `controller/internal/tenant/tenant.go` 🟢 pre-spike
- [ ] T032 [P] [US1] Implement the rate-limit and criticality derivations (config `baseRps: 20`, `minGpuFactor: 0.25`, `criticalPriorityThreshold: 100`) in `controller/internal/reconcile/ratelimit.go` 🟢 pre-spike
- [ ] T033 [US1] Implement ProjectReconciler (watches Project, Department, Queue; waits for Project Ready + `status.namespace`; namespace conflict detection; finalizer `tenancy.f5.com/bundle`; Kubernetes Events on the Project; gauge `bnk_tenant_state{project,state}`) in `controller/internal/reconcile/project_controller.go`, using a `BundleApplier` interface so BNK writes plug in later 🟢 pre-spike
- [ ] T034 [US1] Implement BNK bundle builders for listener/GatewaySettings, HTTPRoute (`/v1/*` → InferencePool), and the rate-limit policy, using only names from `docs/verified-names.md`, with common labels `tenancy.f5.com/project`, `tenancy.f5.com/department`, `app.kubernetes.io/managed-by: bnk-runai-tenancy` and annotation `tenancy.f5.com/source`, in `controller/internal/bnk/builders.go` 🔒 BNK-gated
- [ ] T035 [US1] Implement BundleApplier with server-side apply (field manager `bnk-runai-tenancy`), drift watch on owned kinds, and label-selected deletion, in `controller/internal/bnk/applier.go` 🔒 BNK-gated
- [ ] T036 [US1] Create RBAC per `contracts/tenancy-controller.md` (get/list/watch Project/Department/Queue/Pod; update Project for the finalizer only; CRUD on BNK kinds + HTTPRoute) and the Deployment, in `controller/config/` 🔒 BNK-gated (BNK kinds)
- [ ] T037 [US1] Implement `labctl tenant create|delete|show` as thin wrappers over the **Run:ai API/CLI** (`show` prints the Run:ai project and the generated BNK bundle side by side), in `labctl/cmd/tenant.sh` 🟢 pre-spike (the `show` BNK half is 🔒)
- [ ] T038 [US1] Write the challenge C2 check script verifying bundle presence, labels, 200 via hostname, and removal after delete, in `instruqt/bnk-runai/02-one-tenant/check.sh`, with matching `solve.sh` 🔒 BNK-gated

**Checkpoint**: US1 runs end to end on the spike VM. This is the minimal GTC demo (Santosh steps 1, 2, 4, 8).

---

## Phase 4: User Story 2 — GPU fractions behind one gateway (P1)

**Goal**: Run:ai packs replicas on GPU fractions, and the F5 Endpoint Picker routes by
model state with KV-prefix affinity.

**Independent Test**: quickstart V2. Two pods share one fake GPU, and the
`prefix-heavy` EPP run beats round-robin (SC-002).

- [ ] T039 [P] [US2] Implement `labctl model deploy <tenant> <model> [--replicas N] [--fraction F] [--version v]` submitting a **Run:ai inference workload** (sim image) via the Run:ai API/CLI, in `labctl/cmd/model.sh` 🟢 pre-spike
- [ ] T040 [US2] Add the InferencePool builder (selector `app.kubernetes.io/part-of=model-server` in the tenant namespace, `endpointPickerRef` → F5 EPP from config `eppRef`) to `controller/internal/bnk/builders.go` 🔒 BNK-gated
- [ ] T041 [US2] Implement `labctl routing <tenant> rr|epp` to toggle the HTTPRoute backend between a plain Service (round-robin baseline) and the InferencePool, in `labctl/cmd/routing.sh` 🔒 BNK-gated
- [ ] T042 [P] [US2] Build the Grafana dashboard rows TTFT p50/p95, KV-cache hit rate, and pods per fake GPU, in `platform/observability/dashboards/one-tenant-two-planes.json` 🟢 pre-spike (sim/KAI panels)
- [ ] T043 [US2] Write the challenge C3 check (pods share a GPU via Run:ai; the EPP run TTFT p50 is ≥30% lower than rr, or the cache-hit rate is higher) in `instruqt/bnk-runai/03-fractions-one-door/check.sh` + `solve.sh` 🔒 BNK-gated

---

## Phase 5: User Story 3 — Priority enforced end-to-end (P1)

**Goal**: Run:ai preempts research and BNK sheds research traffic, while prod-chat keeps
its SLO.

**Independent Test**: quickstart V3. prod p95 is ≤1.5× baseline (SC-003).

- [ ] T044 [US3] Add the InferenceObjective builder (Queue priority ≥ `criticalPriorityThreshold` → Critical, else Sheddable; override `tenancy.f5.com/criticality`) to `controller/internal/bnk/builders.go`, with a unit test in `controller/internal/bnk/objective_test.go` 🔒 BNK-gated
- [ ] T045 [P] [US3] Add the `overload` profile, which saturates both tenants and submits a high-priority Run:ai training workload to trigger Run:ai preemption of research, to `loadgen/loadgen.py` and `platform/runai/preempt-workload.yaml` 🟢 pre-spike
- [ ] T046 [P] [US3] Add dashboard panels for Run:ai preemption events/project allocation and BNK shed/429 per tenant to `platform/observability/dashboards/one-tenant-two-planes.json` 🟢 pre-spike (Run:ai half)
- [ ] T047 [US3] Write the challenge C4 check (research pods preempted, research requests shed/throttled, prod p95 ≤1.5× unloaded) in `instruqt/bnk-runai/04-same-priority/check.sh` + `solve.sh` 🔒 BNK-gated

---

## Phase 6: User Story 4 — Per-project token metering (P2)

**Goal**: Tokens or requests per Run:ai project, shown next to GPU allocation.

**Independent Test**: quickstart V4. `bnk_tokens_total` per project, with its `source`
label visible.

- [ ] T048 [US4] Implement the metering exporter per `contracts/metrics.md`: fallback path summing `vllm:prompt_tokens_total`/`vllm:generation_tokens_total` by namespace → project with `source="model-server"`, plus `bnk_requests_total` from BNK stats, in `metering/main.go` 🟢 pre-spike (model-server path) / 🔒 (BNK request stats)
- [ ] T049 [US4] If T009(a) answered yes, add the gateway token path with `source="gateway"` to `metering/gateway.go` 🔒 BNK-gated
- [ ] T050 [P] [US4] Add the "Who used the tokens?" row (tokens/s per project next to the Run:ai project GPU allocation, showing the `source` label) to `platform/observability/dashboards/one-tenant-two-planes.json` 🟢 pre-spike
- [ ] T051 [US4] Write the challenge C5 assignment text, including the disclosure of the token source, and the check in `instruqt/bnk-runai/05-who-used-tokens/` 🟢 pre-spike (text) / 🔒 (check)

**Checkpoint**: US1–US4 complete, which is the full GTC script.

---

## Phase 7: GTC Demo Deliverable (by 2026-10-19)

- [ ] T052 Write the 8-minute script mapping Santosh's 8 steps to `labctl` commands, with the DPU step marked architecture-only, in `demo/gtc/script.md`
- [ ] T053 [P] Write the request-path architecture note (two pages, solid/dashed arrows) in `demo/gtc/architecture.md`
- [ ] T054 Rehearse 3× on the spike VM, record the run, and log timings in `demo/gtc/rehearsal-log.md` (SC-007)

---

## Phase 8: User Story 5 — Safe model rollout (P2)

**Independent Test**: quickstart V5. The split is within ±5% and there are zero client errors.

- [ ] T055 [P] [US5] Add the `rollout` loadgen profile with per-version response tagging to `loadgen/loadgen.py` 🟢 pre-spike
- [ ] T056 [US5] Write the learner-applied InferenceModelRewrite manifests (90/10 and 0/100) in `instruqt/bnk-runai/06-ship-v2/manifests/` 🔒 BNK-gated
- [ ] T057 [US5] Write the challenge C6 check (split ±5%, zero non-2xx) in `instruqt/bnk-runai/06-ship-v2/check.sh` + `solve.sh` 🔒 BNK-gated

## Phase 9: User Story 6 — Per-tenant egress identity (P3)

- [ ] T058 [P] [US6] Deploy a lab-local "model hub" echo server logging source IP in `platform/models/model-hub.yaml` 🟢 pre-spike
- [ ] T059 [US6] Add the EgressGateway builder (NamespaceSelector = tenant ns, SNAT from `egress.snatPoolCidr`) to `controller/internal/bnk/builders.go` 🔒 BNK-gated
- [ ] T060 [US6] Write the challenge C7 check (distinct source IP per tenant in hub logs) in `instruqt/bnk-runai/07-own-exit/check.sh` + `solve.sh` 🔒 BNK-gated

## Phase 10: User Story 7 — Disaggregated serving (P3, stretch)

- [ ] T061 [US7] Adapt the sim's `manifests/disaggregation` into prefill/decode Deployments gang-scheduled by the Run:ai Scheduler (PodGroup) in `platform/models/pd/` 🟢 pre-spike
- [ ] T062 [US7] Configure P/D-aware routing and write the C8 check in `instruqt/bnk-runai/08-disaggregated/` 🔒 BNK-gated (drop to architecture-only if T009(b) fails)

---

## Phase 11: Instruqt Track (after GTC)

- [ ] T063 Measure track start time. If it's over 15 min, add Run:ai/fake-gpu/sim images to a pre-pull step in `instruqt/bnk-runai/track_scripts/setup-bnk` (the `f5/bnk-f5` image stays as is; a new image only if needed)
- [ ] T064 [P] Finalize `instruqt/bnk-runai/track.yml` (new track, quoted teaser ≤255 chars, secrets `BNK_FAR_KEY`/`BNK_JWT`, tabs: terminal, Grafana, Run:ai UI if R5 allows) and `instruqt/config.yml` (custom image VM)
- [ ] T065 [P] Write challenge C1 "Meet the AI Factory" (assignment, setup, check, solve) in `instruqt/bnk-runai/01-meet-the-factory/`
- [ ] T066 Write the track setup script that starts the cluster, applies the license, and waits for `labctl status` green in ≤5 min, in `instruqt/bnk-runai/track_scripts/setup-bnk` (SC-005, target: same as the bnk-ai-kv-lab track)
- [ ] T067 Write the debrief challenge with the architecture slide and honest-claims callouts (DPU, host CPU, Workload Map, GPU isolation) in `instruqt/bnk-runai/09-debrief/`
- [ ] T068 Run all challenge solve+check scripts 3× in a row and `instruqt track test`, logging in `docs/validation/track-test.md` (SC-006)
- [ ] T069 Do a full browser walkthrough of the Instruqt track (terminal attach time, Grafana layout, timing), logging findings in `docs/validation/walkthrough.md`

---

## Phase 12: Polish, Packaging & Publish

- [ ] T070 [P] Create the Helm chart (controller + metering + dashboards + RBAC, values per `contracts/tenancy-controller.md`) in `charts/bnk-runai-tenancy/`
- [ ] T071 [P] Write the validation matrix with pinned versions (BNK, Run:ai images, fake-gpu, sim, K8s) in `docs/validation-matrix.md` (brief W3)
- [ ] T072 Write the public README (what it does, Run:ai objects watched, BNK objects produced, quickstart, honest limits) and `CONTRIBUTING.md`/`NOTICE` in the repo root
- [ ] T073 **Publish the controller as a public Apache 2.0 repo** (e.g. `f5devcentral/bnk-runai-tenancy`): get F5 open-source approval, run the secret scan (T015) on full history, and push the controller, metering, chart, and docs (lab-only content optional)
- [ ] T074 **Docs PR to `dsx-ai-factory/run-ai`**: add F5 BNK 2.4 as a conformant Gateway API controller example to `docs/multi-tenant/installation-and-configuration/configuration/networking/kubernetes-gateway-api.md` (install, GatewayClass, routing of control plane + inference hosts), with DCO sign-off (`git commit -s`), following their CONTRIBUTING (open an issue first)
- [ ] T075 **Issue/Discussion on `dsx-ai-factory/run-ai`** proposing a "gateway / service-plane integration" pattern (tenant = Run:ai Project → gateway tenant, queue priority → inference objective, per-project metering), linking the public repo (T073) and the docs PR (T074)
- [ ] T076 [P] Brain sync: store the verified names and spike outcomes via `add_memory` (project RunAI) and file a wiki note `~/brain/wiki/` on the BNK+Run:ai integration, then run `bash ~/brain/tools/sync.sh "bnk-runai lab"`

---

## Dependencies & Execution Order

```
Phase 0 (spike) ──gate T011──▶ all 🔒 tasks
Phase 1 Setup ──▶ Phase 2 Foundational ──▶ US1 ──▶ US2 ──▶ US3 ──▶ US4 ──▶ GTC (Phase 7)
                                            └──────────▶ US5, US6, US7 (independent after US1)
GTC ──▶ Phase 11 Instruqt ──▶ Phase 12 Publish (T073 → T074 → T075)
```

- Phases 1–2 🟢 tasks run **in parallel with the spike**.
- US2–US7 each depend on US1 (bundle + applier) but not on each other, except that US4's
  dashboard row reuses the US2 dashboard file.
- T074/T075 depend on T073 (the public repo link) and a working BNK Gateway config (T034).

## Parallel Opportunities

**While the spike runs (today):** T012–T021, T024–T033, T039, T042, T045, T046, T048
(model-server path), T050, T055, T058, T061. That's about 25 tasks with zero BNK
dependency.

**US1 parallel batch:**
```
T027, T028, T029, T030 (tests)  ‖  T031, T032 (models/derivations)
```

**After the gate:** T040, T044, T059 are separate builder functions. They share
`builders.go`, so split them into `builders_pool.go` / `builders_objective.go` /
`builders_egress.go` to keep them parallel.

## Implementation Strategy

1. **MVP = Phase 0 + US1**: Santosh steps 1, 2, 4, 8 live. It's already a credible
   NVIDIA demo.
2. **GTC = US1–US4** (Phase 7 by 2026-10-19). If the token answer is "no", use the
   fallback with disclosure.
3. **Instruqt = all stories + Phase 11**, after GTC.
4. **Publish (Phase 12)**: public repo first, then the docs PR and issue to Run:ai.

## Gate Summary

| Tag | Count |
|---|---|
| Tasks tagged 🟢 pre-spike (some partially) | ~33 |
| Tasks tagged 🔒 BNK-gated (some partially) | ~22 |
| Total tasks | 76 |
