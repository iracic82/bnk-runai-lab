---
description: "Task list for BNK 2.4 × Open-Source Run:ai lab"
---

# Tasks: BNK 2.4 × Open-Source Run:ai — "One Tenant, Two Planes" Lab

**Input**: Design documents from `/specs/001-bnk-runai-lab/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: The plan requires Go unit and envtest tests for the controller, plus
check scripts per challenge (constitution quality gates). They are included.

**Organization**: Follows Santosh's brief. Phase 0 spike (hard gate), Setup, Foundational,
W1 (US1+US2), W2 (US3), W5 GTC demo, W3 (US5), W4 (US4), Instruqt, publish. Phases marked
**OPTIONAL EXTRA** are beyond the brief.

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

- [X] T012 Create the repo skeleton per plan.md (`controller/ metering/ charts/ platform/ labctl/ loadgen/ instruqt/ demo/gtc/ docs/`), plus `.gitignore` (excluding `*.jwt`, `*far*`, `.env`) and an Apache-2.0 `LICENSE` at repo root 🟢 pre-spike
- [X] T013 [P] Initialize the Go module `github.com/f5devcentral/bnk-runai-tenancy` in `controller/go.mod` (Go 1.24, controller-runtime, client-go), with a `Makefile` providing `build test envtest image` targets 🟢 pre-spike
- [X] T014 [P] Create `platform/versions.env` pinning the bnk-ai-kv-lab and bnk-deploy git refs, Run:ai image digests (GAR), fake-gpu-operator, llm-d-inference-sim, kube-prometheus-stack, Gateway API, and GAIE CRD versions (BNK versions left as `TODO-S0`) 🟢 pre-spike
- [X] T015 [P] Add `scripts/secret-scan.sh` (gitleaks or a grep for JWT/FAR patterns) and wire it into a pre-commit hook in `.githooks/pre-commit` 🟢 pre-spike

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
- [X] T026 Scaffold the controller manager in `controller/cmd/manager/main.go` (scheme registration for unstructured `kai.resources/v1alpha1` Project/Department and `scheduling.run.ai` Queue, leader election, metrics on `:8080`, health probes) 🟢 pre-spike

**Checkpoint**: On the spike sandbox, `platform/install.sh` brings up Run:ai, fake GPU, and observability over BNK, and `labctl status` is all green.

---

## Phase 3: W1 · User Story 1 — Tenant defined once (P1) 🎯 MVP / GTC core

**Goal**: Create a Run:ai Project, and a BNK tenant bundle appears. Change the quota,
and the limit changes. Delete the Project, and the bundle is gone.

**Independent Test**: quickstart V1/V1b. The bundle appears within 30 s, is labelled
with the project, `curl` gets 200 via the tenant hostname, and deleting the Project
removes it within 30 s.

### Tests for US1

- [X] T027 [P] [US1] Envtest that installs the Run:ai cluster Project/Department CRDs (`kai.resources`, as shipped in `runai-chart`) (copied to `controller/test/crds/`) and asserts that Project → bundle builder input mapping reads the namespace from `status.namespace`, never from the name, in `controller/test/envtest/project_mapping_test.go` 🟢 pre-spike
- [X] T028 [P] [US1] Unit tests for the rate-limit derivation `rps = baseRps × max(deserved, minGpuFactor)`, the override annotation `tenancy.f5.com/rps`, and "Project without a GPU queue gets `rps = base_rps × 0.25` (never unlimited)", in `controller/internal/reconcile/ratelimit_test.go` 🟢 pre-spike
- [X] T029 [P] [US1] Unit test for "Two Projects resolving to one namespace → Error state for both, no writes", in `controller/internal/reconcile/conflict_test.go` 🟢 pre-spike
- [X] T030 [P] [US1] Envtest for finalizer lifecycle: the `tenancy.f5.com/bundle` finalizer is added on Ready, and on deletion the bundle is deleted before the finalizer is removed, in `controller/test/envtest/finalizer_test.go` 🟢 pre-spike

### Implementation for US1

- [X] T031 [P] [US1] Implement the TenantSpec model (project, department, namespace, gpuDeserved, priority, overrides) plus parsing from unstructured Project/Department in `controller/internal/tenant/tenant.go` 🟢 pre-spike
- [X] T032 [P] [US1] Implement the rate-limit and criticality derivations (config `baseRps: 20`, `minGpuFactor: 0.25`, `criticalPriorityThreshold: 100`) in `controller/internal/reconcile/ratelimit.go` 🟢 pre-spike
- [X] T033 [US1] Implement ProjectReconciler (watches Project, Department, Queue; waits for Project Ready + `status.namespace`; namespace conflict detection; finalizer `tenancy.f5.com/bundle`; Kubernetes Events on the Project; gauge `bnk_tenant_state{project,state}`) in `controller/internal/reconcile/project_controller.go`, using a `BundleApplier` interface so BNK writes plug in later 🟢 pre-spike
- [ ] T034 [US1] Implement BNK bundle builders for listener/GatewaySettings, HTTPRoute (`/v1/*` → InferencePool), and the rate-limit policy, using only names from `docs/verified-names.md`, with common labels `tenancy.f5.com/project`, `tenancy.f5.com/department`, `app.kubernetes.io/managed-by: bnk-runai-tenancy` and annotation `tenancy.f5.com/source`, in `controller/internal/bnk/builders.go` 🔒 BNK-gated
- [ ] T035 [US1] Implement BundleApplier with server-side apply (field manager `bnk-runai-tenancy`), drift watch on owned kinds, and label-selected deletion, in `controller/internal/bnk/applier.go` 🔒 BNK-gated
- [ ] T036 [US1] Create RBAC per `contracts/tenancy-controller.md` (get/list/watch Project/Department/Queue/Pod; update Project for the finalizer only; CRUD on BNK kinds + HTTPRoute) and the Deployment, in `controller/config/` 🔒 BNK-gated (BNK kinds)
- [ ] T037 [US1] Implement `labctl tenant create|delete|show` as thin wrappers over the **Run:ai API/CLI** (`show` prints the Run:ai project and the generated BNK bundle side by side), in `labctl/cmd/tenant.sh` 🟢 pre-spike (the `show` BNK half is 🔒)
- [ ] T038 [US1] Write the challenge C2 check script verifying bundle presence, labels, 200 via hostname, and removal after delete, in `instruqt/bnk-runai/02-one-tenant/check.sh`, with matching `solve.sh` 🔒 BNK-gated

**Checkpoint**: US1 runs end to end on the spike VM. This is the minimal GTC demo (Santosh steps 1, 2, 4, 8).

---

## Phase 4: W1 · User Story 2 — Isolation before any GPU cycle (P1)

**Goal**: tenant-a and tenant-b share one GPU (Run:ai fractions). tenant-b is throttled at
BNK at its quota-derived limit, and tenant-a is unaffected (brief steps 3, 5).

**Independent Test**: Both pods are on one fake GPU. Under load, tenant-b gets 429s and
tenant-a has zero errors with p95 within 10% of unloaded (SC-002).

- [ ] T039 [P] [US2] Implement `labctl model deploy <tenant> <model> [--fraction F]`, submitting a **Run:ai inference workload** (sim image) via the Run:ai API/CLI, in `labctl/cmd/model.sh` 🟢 pre-spike
- [ ] T040 [US2] Add the rate-limit iRule builder (`F5BigCneIrule` + `NetPolicy` attached to the tenant listener; limit from T032) to `controller/internal/bnk/builders_ratelimit.go` 🔒 BNK-gated
- [ ] T041 [US2] Baseline (not the integration): point the tenant HTTPRoute at an InferencePool + `F5EPP` over the Run:ai inference pods, in `controller/internal/bnk/builders_pool.go`, with a comment that cites the brief ("already compatible") 🔒 BNK-gated
- [ ] T042 [P] [US2] Add the `two-tenants` loadgen profile (tenant-a steady, tenant-b above its limit; reports 429s and p95 per tenant) to `loadgen/loadgen.py` 🟢 pre-spike
- [ ] T043 [P] [US2] Add dashboard row "Isolation": per-tenant req/s, 429/s, p95, plus pods per fake GPU, in `platform/observability/dashboards/tenant-defined-once.json` 🟢 pre-spike (Run:ai half)
- [ ] T044 [US2] Write the challenge C3 check (pods share a GPU; tenant-b 429s; tenant-a zero errors, p95 within 10%) in `instruqt/bnk-runai/03-isolation/check.sh` + `solve.sh` 🔒 BNK-gated

---

## Phase 5: W2 · User Story 3 — Tokens metered per Run:ai project (P1) 🎯

**Goal**: `bnk_tokens_total{project,department,model,direction}` counted at BNK, keyed to the Run:ai project at ingress, beside Run:ai GPU allocation (brief step 6).

**Independent Test**: SC-003. `bnk_tokens_total` per project, with its `source`
label visible.

- [ ] T045 [US3] Implement the gateway metering iRule (tag the request with the project at ingress; read `usage.prompt_tokens`/`completion_tokens` from the OpenAI-compatible response or stream; HSL/stats out), modelled on the Lambda lab's per-user metering iRule, in `controller/internal/bnk/irules/metering.tcl` 🔒 BNK-gated
- [ ] T046 [US3] Implement the metering exporter turning iRule output into `bnk_tokens_total{project,department,model,direction}` and `bnk_requests_total` (request-count fallback if T009(a) fails, with `source` label and disclosure), in `metering/main.go` 🔒 BNK-gated
- [ ] T047 [P] [US3] Add the "Who used the tokens?" row (tokens/s per project next to the Run:ai project GPU allocation, showing the `source` label) to `platform/observability/dashboards/one-tenant-two-planes.json` 🟢 pre-spike
- [ ] T048 [US3] Write the challenge C5 assignment text, including the disclosure of the token source, and the check in `instruqt/bnk-runai/05-who-used-tokens/` 🟢 pre-spike (text) / 🔒 (check)

**Checkpoint**: US1–US4 complete, which is the full GTC script.

---

## Phase 6: W5 · GTC Demo Deliverable (by 2026-10-19)

- [ ] T049 Write the 8-minute script following the brief's 8 steps exactly (Run:ai UI for steps 2–3 and 8; `kubectl get` BNK CRs for step 4; Grafana for steps 5–6; BlueField-3 recording for step 7; request-count fallback slide), in `demo/gtc/script.md`
- [ ] T050 [P] Write the request-path architecture note (two pages, solid/dashed arrows) in `demo/gtc/architecture.md`
- [ ] T051 Rehearse 3× on the spike VM, record the run, and log timings in `demo/gtc/rehearsal-log.md` (SC-007)

---

## Phase 7: W3 · User Story 5 — Installable beside Run:ai (P2)

- [ ] T052 [P] [US5] Create the Helm chart (controller + metering + dashboards + RBAC read-only on Run:ai objects; values per `contracts/tenancy-controller.md`) in `charts/bnk-runai-tenancy/`
- [ ] T053 [US5] Prove `helm install` on a cluster already running the open-source Run:ai chart, then pass US1, and log it in `docs/validation/helm-install.md`
- [ ] T054 [P] [US5] Write the validation matrix (BNK, Run:ai control plane + cluster chart incl. scheduler/resource management, GPU fractioning operator, K8s, CNI) with pinned versions, in `docs/validation-matrix.md`

## Phase 8: W4 · User Story 4 — Workload Map–ready telemetry (P2)

- [ ] T055 [US4] Document the per-project telemetry schema (request rate, token throughput, edge TTFT, egress destinations; labels project/department/model) in `docs/workload-map-schema.md`, flagging that the ingestion format awaits an NVIDIA contact
- [ ] T056 [US4] Populate every schema field in Grafana (edge TTFT from BNK stats or the iRule; egress destinations from BNK egress logs), in `platform/observability/dashboards/tenant-defined-once.json` 🔒 BNK-gated

## Phase 9: OPTIONAL EXTRAS (beyond the brief; Instruqt only, after D1–D3)

- [ ] T057 [P] Priority-aligned shedding: InferenceObjective from Run:ai priority + Run:ai preemption scenario, in `instruqt/bnk-runai/extras/priority/` 🔒 BNK-gated
- [ ] T058 [P] v2 rollout via InferenceModelRewrite, in `instruqt/bnk-runai/extras/rollout/` 🔒 BNK-gated
- [ ] T059 [P] Egress identity per project (`F5SPKEgress` + Snatpool), in `instruqt/bnk-runai/extras/egress/` 🔒 BNK-gated

## Phase 10: Instruqt Track (after GTC)

- [ ] T060 Measure track start time. If it's over 15 min, add Run:ai/fake-gpu/sim images to a pre-pull step in `instruqt/bnk-runai/track_scripts/setup-bnk` (the `f5/bnk-f5` image stays as is; a new image only if needed)
- [ ] T061 [P] Finalize `instruqt/bnk-runai/track.yml` (new track, quoted teaser ≤255 chars, secrets `BNK_FAR_KEY`/`BNK_JWT`, tabs: terminal, Grafana, Run:ai UI if R5 allows) and `instruqt/config.yml` (custom image VM)
- [ ] T062 [P] Write challenge C1 "Meet the AI factory" (Run:ai UI, GPU nodes; assignment, setup, check, solve) in `instruqt/bnk-runai/01-meet-the-factory/`
- [ ] T063 Write the track setup script that starts the cluster, applies the license, and waits for `labctl status` green in ≤5 min, in `instruqt/bnk-runai/track_scripts/setup-bnk` (SC-005, target: same as the bnk-ai-kv-lab track)
- [ ] T064 Write the debrief challenge with the brief's request-path diagram, the BlueField-3 recording (host CPU flat, DPU ingress), and callouts for what is simulated in `instruqt/bnk-runai/09-debrief/`
- [ ] T065 Run all challenge solve+check scripts 3× in a row and `instruqt track test`, logging in `docs/validation/track-test.md` (SC-006)
- [ ] T066 Do a full browser walkthrough of the Instruqt track (terminal attach time, Grafana layout, timing), logging findings in `docs/validation/walkthrough.md`

---

## Phase 11: Publish — the NVIDIA packet (brief)

- [ ] T067 Write the public README (what it does, Run:ai objects watched, BNK objects produced, quickstart, honest limits) and `CONTRIBUTING.md`/`NOTICE` in the repo root
- [ ] T068 **Publish the controller as a public Apache 2.0 repo** (e.g. `f5devcentral/bnk-runai-tenancy`): get F5 open-source approval, run the secret scan (T015) on full history, and push the controller, metering, chart, and docs (lab-only content optional)
- [ ] T069 **Docs PR to `dsx-ai-factory/run-ai`**: add F5 BNK 2.4 as a conformant Gateway API controller example to `docs/multi-tenant/installation-and-configuration/configuration/networking/kubernetes-gateway-api.md` (install, GatewayClass, routing of control plane + inference hosts), with DCO sign-off (`git commit -s`), following their CONTRIBUTING (open an issue first)
- [ ] T070 **Issue/Discussion on `dsx-ai-factory/run-ai`** proposing a "gateway / service-plane integration" pattern (tenant = Run:ai Project → gateway tenant, queue priority → inference objective, per-project metering), linking the public repo (T073) and the docs PR (T074)
- [ ] T071 [P] Write the 2-page architecture note from the brief's request-path diagram and add the EPP-with-Dynamo baseline doc link, in `docs/architecture-note.md`
- [ ] T072 [P] Brain sync: store the verified names and spike outcomes via `add_memory` (project RunAI) and file a wiki note `~/brain/wiki/` on the BNK+Run:ai integration, then run `bash ~/brain/tools/sync.sh "bnk-runai lab"`

---

## Dependencies & Execution Order

```
Phase 0 spike ──gate──▶ all 🔒 tasks
Setup ─▶ Foundational ─▶ W1 (US1 → US2) ─▶ W2 (US3) ─▶ W5 GTC demo (by Oct 19)
                                    └──▶ W3 (US5), W4 (US4) ─▶ Instruqt ─▶ Publish packet
Optional extras: only after the GTC demo and the packet are done.
```

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
| Total tasks | 72 |
