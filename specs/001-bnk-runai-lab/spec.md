# Feature Specification: BNK 2.4 × Open-Source NVIDIA Run:ai — Tenant Defined Once

**Feature Branch**: `001-bnk-runai-lab`
**Created**: 2026-10-15 · **Revised**: 2026-10-15 (re-anchored on Santosh's engineering brief)
**Status**: Draft
**Source of truth**: "BNK + Open-Source Run:ai — Engineering Brief for Pre-Sales" (Santosh
Erram, 2026-10-14). Where this spec and the brief differ, the brief wins. The exceptions are
the identifier corrections in `docs/verified-names.md`, which the brief itself asked us to
verify.

## Goal (from the brief)

Get F5 listed as the **DSX OS service-plane component** for open-source NVIDIA Run:ai, by
showing NVIDIA at GTC Berlin (20–22 Oct) that **BNK consumes Run:ai's tenant model and feeds
its visibility layer**.

The integration is **operational, not functional**. It does not change the BNK data plane:

1. A tenant is **defined once in Run:ai** and exists in BNK (W1).
2. **Tokens are metered per Run:ai project** (W2).
3. Tenant traffic telemetry is shaped for **Workload Map** (W4; Grafana until the format
   is known).

**Baseline, not the integration**: BNK's Endpoint Picker already routes to Run:ai-scheduled
inference pods through the Gateway API Inference Extension (and to Dynamo). It is shown and
documented as "already compatible". It is not presented as what we built.

**Takeaway the audience must leave with** (brief, verbatim): *a tenant is defined once, in
Run:ai, and exists on the DPU; traffic isolation and metering happen before any GPU cycle;
host CPUs are untouched.*

## Deliverables

| ID | Deliverable | Audience |
|---|---|---|
| D1 | GTC demo, under 8 min, recorded, following the brief's 8-step script | NVIDIA |
| D2 | NVIDIA packet: public Apache 2.0 controller repo, Helm chart + validation matrix, 2-page architecture note, recording, EPP-with-Dynamo baseline doc | NVIDIA engineering |
| D3 | Instruqt track: the same story hands-on, with simulated GPUs | partners, SEs, customers |

## User Scenarios & Testing *(mandatory)*

Tenants follow the brief: **tenant-a** (2 GPU quota) and **tenant-b** (0.5 GPU quota),
in Department `acme`.

### User Story 1 — Tenant defined once (P1) 🎯 W1 · brief steps 2, 4, 8

A platform admin creates a project in Run:ai. BNK gets that tenant automatically: ingress
virtual server/hostname, namespace binding, and a rate limit derived from the project's
GPU quota. Deleting the project in Run:ai removes the BNK tenant.

**Independent Test**: Create tenant-a and tenant-b in the Run:ai UI. Within 30 s,
`kubectl get` shows the BNK tenant objects, labelled with the Run:ai project and department.
Delete tenant-b in Run:ai, and its BNK objects and endpoint are gone within 30 s.

**Acceptance Scenarios**:
1. **Given** Run:ai, BNK and the controller are running, **When** a project is created in
   Run:ai, **Then** a BNK tenant (ingress VS/hostname, route, rate limit) exists, labelled
   with the Run:ai project and department.
2. **Given** a project's GPU quota changes in Run:ai, **When** the controller reconciles,
   **Then** the tenant's rate limit changes in proportion. An explicit annotation overrides it.
3. **Given** a project is deleted in Run:ai, **When** the controller reconciles, **Then**
   the BNK tenant and its ingress disappear.
4. **Given** someone deletes a BNK tenant object by hand, **When** the controller
   reconciles, **Then** it is recreated (Run:ai stays the source of truth).

### User Story 2 — Isolation before any GPU cycle (P1) · brief steps 3, 5

Two tenants share one GPU through Run:ai GPU fractions. When both send traffic, tenant-b
is throttled at its quota-derived limit **at BNK**, and tenant-a is unaffected.

**Independent Test**: Both inference workloads run on fractions of the same (simulated) GPU.
A load run against both shows 429s for tenant-b at its limit, with flat latency and zero
errors for tenant-a.

**Acceptance Scenarios**:
1. **Given** one inference workload per project at 0.5 GPU, **When** the learner inspects
   pods, **Then** both run on the same GPU, placed by the Run:ai Scheduler.
2. **Given** traffic above tenant-b's limit, **When** it reaches BNK, **Then** BNK rejects
   the excess before it reaches the GPU node, and tenant-a's p95 latency is unchanged.

### User Story 3 — Tokens metered per Run:ai project (P1) 🎯 W2 · brief step 6

BNK counts prompt and completion tokens per request, keyed to the Run:ai project at
ingress, and exposes `bnk_tokens_total{project,department,model,direction}`. A Grafana panel
shows it beside Run:ai's GPU allocation for the same projects.

**Independent Test**: After a traffic run, the panel shows token counters per project,
proportional to traffic sent, beside each project's GPU allocation.

**Acceptance Scenarios**:
1. **Given** traffic to both tenants, **When** the dashboard is opened, **Then**
   `bnk_tokens_total` rises per project and direction.
2. **Given** BNK 2.4 cannot count tokens at the gateway, **When** the demo runs, **Then**
   the panel shows request counts per project, and the slide says so (brief fallback).

### User Story 4 — Workload Map–ready telemetry (P2) · W4

Per-project request rate, token throughput, edge TTFT and egress destinations are exported
in one documented schema, shown in Grafana now and ready to feed NVIDIA Workload Map once
NVIDIA shares the ingestion format.

**Independent Test**: A documented metric schema exists, and every field is populated in
Grafana after a traffic run.

### User Story 5 — Installable beside Run:ai (P2) · W3

One Helm chart installs the controller and metering beside the open-source Run:ai chart.
The validation matrix records the tested versions of BNK, Run:ai (including its scheduler
and resource management) and the GPU fractioning operator.

**Independent Test**: `helm install` on a cluster that already runs Run:ai, then US1
passes. The matrix file lists the pinned versions.

### Baseline (shown, not built) — Run:ai-scheduled inference behind the BNK Endpoint Picker

The routes from US1 point at a BNK InferencePool with the F5 Endpoint Picker over the
Run:ai inference pods. One lab step shows that this already works and links the
EPP-with-Dynamo document. No new integration is claimed.

### Optional Instruqt extras (beyond the brief; not in the GTC demo)

Shown only if time allows after D1–D3: priority-aligned shedding, v2 model rollout,
per-project egress identity. They're clearly marked as extras.

### Edge Cases

- A project without a GPU quota gets a minimal default rate limit, never unlimited.
- Two projects resolving to one namespace: an error condition is raised and no BNK config
  is written.
- A controller restart mid-reconcile leaves no duplicates or orphans.
- BNK license missing or expired: setup fails fast with a clear message.
- All tenant pods gone: BNK returns a fast 503, not a timeout.

## Requirements *(mandatory)*

- **FR-001**: Real BNK 2.4 and the full open-source NVIDIA Run:ai platform (control plane +
  cluster). Learners and presenters use the Run:ai UI or CLI.
- **FR-002**: An open-source (Apache 2.0) controller creates, updates and deletes per-project
  BNK tenants from Run:ai projects and queues, with RBAC that is read-only on Run:ai objects.
- **FR-003**: The per-tenant rate limit is derived from the project's Run:ai GPU quota,
  with an annotation override.
- **FR-004**: Token counters per project, or request counters with disclosure, labelled
  `project`, `department`, `model`, `direction`.
- **FR-005**: One Helm chart plus a validation matrix with pinned versions.
- **FR-006**: The telemetry schema is documented for Workload Map.
- **FR-007**: No change to the BNK data plane, no scheduling functions, no east-west scope.
- **FR-008**: Claims the environment can't prove are stated as such: the DPU and host-CPU
  claims in Instruqt are shown on BlueField-3 hardware or as a recording.
- **FR-009**: The GTC script follows the brief's 8 steps and runs under 8 minutes.

## Success Criteria *(mandatory)*

- **SC-001**: Project created in Run:ai → BNK tenant serving in ≤30 s; project deleted →
  BNK tenant gone in ≤30 s.
- **SC-002**: Under load, tenant-b is throttled at its limit while tenant-a has zero errors
  and p95 within 10% of unloaded.
- **SC-003**: Token (or request) counters per project match the load generator's totals
  within 2%.
- **SC-004**: The demo runs end to end in ≤8 min, recorded by 2026-10-19.
- **SC-005**: Helm install beside Run:ai succeeds on a clean cluster. The matrix is published.
- **SC-006**: The Instruqt track passes `instruqt track test` and its check scripts 3× in a row.

## Assumptions

- Instruqt uses simulated GPUs (fake-gpu-operator) and a vLLM-compatible simulator. Real
  BlueField-3 and GPUs are only on the partner cluster (brief: vCluster or Rafay), which
  Santosh is confirming.
- Identifiers follow `docs/verified-names.md`, not the pre-release names in the brief:
  - The project namespace is read from the Run:ai project status.
  - The controller watches projects and pods, not `InferenceWorkload`.
- BNK license and install reuse the Lambda POC setup (team secrets, bnk-deploy).
- Workload Map ingestion needs an NVIDIA engineering contact (brief open question).
