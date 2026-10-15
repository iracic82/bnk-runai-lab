# Feature Specification: BNK 2.4 × Open-Source Run:ai — "One Tenant, Two Planes" Lab

**Feature Branch**: `001-bnk-runai-lab`
**Created**: 2026-10-15
**Status**: Draft
**Input**: User description: "New Instruqt track with real BNK 2.4 and open-source NVIDIA
Run:ai (simulated GPUs, no real GPU hardware). BNK must shine: every BNK 2.4 capability
fits a Run:ai moment and makes sense. Covers Santosh's GTC Berlin brief (tenancy
controller, per-project metering) and is appealing enough to get F5 listed in the
NVIDIA DSX ecosystem."

## Story

**Acme AI Factory** runs one shared GPU cluster. The platform team uses Run:ai to
decide *who gets GPUs*. The learner is the platform engineer who adds BNK so the same
tenant model decides *who gets tokens, at what priority, over which path*. A tenant is
defined once in Run:ai and appears in BNK automatically.

Two tenants carry the whole story:

- **prod-chat**: high priority, a guaranteed GPU quota, a customer-facing chat model.
- **research**: low priority, a fractional GPU quota, batch and experimental models.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Tenant defined once (Priority: P1) 🎯 MVP / GTC core

As a platform engineer, I create a Run:ai project with a GPU quota, and BNK
automatically gets an isolated front door for that tenant with rate limits derived
from the quota. Deleting the project removes it.

**Why this priority**: This is Santosh's headline and the reason NVIDIA would list F5.
It works without any other story.

**Independent Test**: Create projects `prod-chat` (quota 2 GPU) and `research`
(quota 0.5 GPU). Within 30 seconds, each has its own BNK listener, route, and rate
limit, labelled with the Run:ai project. Delete `research`, and its BNK objects are
gone within 30 seconds.

**Acceptance Scenarios**:

1. **Given** BNK, Run:ai, and the tenancy controller are running, **When** the
   learner creates a Run:ai project with a GPU quota, **Then** a BNK tenant (gateway
   listener/VIP, route, rate-limit policy) exists, labelled with the project and
   department names.
2. **Given** a project's GPU quota is changed in Run:ai, **When** the controller
   reconciles, **Then** the tenant's request/token rate limit changes proportionally.
3. **Given** a project is deleted, **When** the controller reconciles, **Then** all
   BNK objects for that tenant are removed and its endpoint stops answering.

---

### User Story 2 — GPU fractions behind one gateway, routed by model state (Priority: P1)

As a platform engineer, I deploy model servers through Run:ai (the Run:ai Scheduler places them on
fractional simulated GPUs). BNK's InferencePool and F5 Endpoint Picker route each
request to the best replica using live model-server state, including KV-cache prefix
affinity.

**Why this priority**: This shows BNK 2.4's flagship inference feature on exactly the
pods Run:ai scheduled. It's the "composition" proof.

**Independent Test**: Two replicas of one model run on GPU fractions of the same
simulated GPU. A load run with repeated system prompts shows a higher prefix-cache
hit rate and lower TTFT with the Endpoint Picker than with plain round-robin, on a
side-by-side dashboard.

**Acceptance Scenarios**:

1. **Given** replicas were placed by the Run:ai Scheduler with `gpu-fraction`, **When** the learner
   inspects pods, **Then** two or more pods share one simulated GPU, scheduled via
   the Run:ai project queue.
2. **Given** an InferencePool selects the tenant's model pods, **When** traffic with
   shared prefixes is sent, **Then** cache hit rate and TTFT on the dashboard beat the
   round-robin baseline run.
3. **Given** Run:ai scales or moves a replica, **When** traffic continues, **Then** BNK
   tracks the new endpoint set with no client-visible errors.

---

### User Story 3 — Priority enforced end-to-end (Priority: P1)

Under contention, Run:ai preempts low-priority research workloads. BNK enforces the
same intent on the data path: prod-chat requests keep their latency SLO while research
requests are shed or throttled first.

**Why this priority**: This is the most memorable moment of the demo, with one business
intent enforced at both the scheduler and the gateway.

**Independent Test**: Start research and prod load together, then trigger overload.
The dashboard shows research pods preempted by Run:ai and research requests shed by BNK,
while prod-chat p95 TTFT stays within its target.

**Acceptance Scenarios**:

1. **Given** both tenants are under load, **When** cluster GPU demand exceeds
   capacity, **Then** Run:ai preempts research workloads per queue priority.
2. **Given** model servers are saturated, **When** requests arrive, **Then** BNK
   admits prod-chat (critical objective) and sheds or throttles research
   (sheddable/standard objective).
3. **Given** contention ends, **When** research capacity returns, **Then** research
   traffic is served again without manual action.

---

### User Story 4 — Per-project token metering (Priority: P2)

As a platform owner, I see tokens (or requests, if BNK can't count tokens yet)
consumed per Run:ai project and department, side by side with that project's GPU
allocation.

**Why this priority**: This is the chargeback story and Santosh's W2. It's valuable,
but it depends on an open product question.

**Independent Test**: After traffic, a Grafana panel shows
`bnk_tokens_total{project,department,model,direction}` (or the request-count
fallback) per project, next to the Run:ai project allocation for the same project.

**Acceptance Scenarios**:

1. **Given** traffic to both tenants, **When** the learner opens the dashboard,
   **Then** per-project prompt and completion token counters increase in proportion to
   traffic sent.
2. **Given** BNK 2.4 cannot count tokens, **When** the lab runs, **Then** the panel
   shows request counts and the lab text says so explicitly.

---

### User Story 5 — Safe model rollout (Priority: P2)

Run:ai deploys v2 of a model alongside v1. BNK's InferenceModelRewrite sends a
weighted share of traffic to v2 under the same client-facing model name, then shifts
it to 100%.

**Why this priority**: This is a common operator workflow and shows a distinct BNK 2.4
capability triggered by a Run:ai deployment.

**Independent Test**: Clients call model name `chat`. Traffic splits 90/10 to v1/v2,
then 0/100, with no client change and no errors.

**Acceptance Scenarios**:

1. **Given** v1 and v2 deployed via Run:ai, **When** a 90/10 rewrite is applied,
   **Then** observed traffic split is 90/10 ±5%.
2. **Given** the rewrite moves to 100% v2, **When** v1 is deleted in Run:ai,
   **Then** no client requests fail.

---

### User Story 6 — Per-tenant egress identity (Priority: P3)

Each Run:ai project pulls models and calls external APIs through a BNK EgressGateway
with its own source identity, so outbound traffic is attributable per tenant.

**Why this priority**: It's a security and audit story that completes the tenant
picture. It's nice to have.

**Independent Test**: A pod in each project calls a lab-local "model hub" endpoint.
The hub's log shows a different source IP per tenant.

**Acceptance Scenarios**:

1. **Given** an EgressGateway per tenant namespace, **When** pods make outbound
   calls, **Then** the destination sees the tenant-specific SNAT address.

---

### User Story 7 — Disaggregated serving (Priority: P3, stretch)

Run:ai gang-schedules prefill and decode workers as one unit. BNK routes the two phases to
their separately scaled pools.

**Why this priority**: This is advanced and depends on simulator support. If it can't
be shown honestly, it becomes architecture-only content.

**Independent Test**: Request traces show prefill served by the prefill pool and decode
by the decode pool. Scaling decode does not disturb prefill routing.

---

### Edge Cases

- A Run:ai project is created without a GPU quota: the tenant gets a default minimal
  rate limit, not unlimited.
- Two projects map to the same namespace: the controller refuses and sets a status
  condition, with no partial BNK config.
- The controller is restarted mid-reconcile: no duplicate or orphaned BNK objects
  (idempotent reconciliation).
- BNK license is missing or expired at lab start: the setup script fails fast with a
  clear message, rather than letting the track silently degrade.
- All replicas of a tenant are preempted: BNK returns a fast, clean error (503 with
  reason), not a timeout.
- The learner deletes a BNK object the controller owns: the controller recreates it
  (drift correction is shown as a feature).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The lab MUST run real BNK 2.4 and the **full open-source NVIDIA Run:ai
  platform** (control plane UI/API + cluster chart) on simulated GPUs. Learners create
  projects and deploy workloads through the Run:ai UI or CLI.
- **FR-002**: An open-source (Apache 2.0) tenancy controller MUST create, update, and
  delete per-tenant BNK configuration from Run:ai Projects and Queues.
- **FR-003**: Tenant rate limits MUST be derived from the Run:ai queue GPU quota, with
  a per-project annotation override.
- **FR-004**: Model replicas MUST be scheduled by the Run:ai Scheduler with GPU fractions and be
  selected by a BNK InferencePool via the tenant namespace and the
  `kai.scheduler/queue` label.
- **FR-005**: The lab MUST demonstrate Endpoint Picker routing against a round-robin
  baseline with measurable differences on dashboards.
- **FR-006**: Run:ai queue priority MUST map to BNK inference objective criticality,
  and the mapping MUST be produced by the controller.
- **FR-007**: The lab MUST expose per-project token counters, or request counters with
  explicit disclosure, alongside per-project GPU allocation.
- **FR-008**: The lab MUST demonstrate model-version rollout via InferenceModelRewrite
  triggered by a Run:ai deployment.
- **FR-009**: Every challenge MUST have setup, check, and solve scripts, and be
  completable via a single `labctl` CLI.
- **FR-010**: Lab text MUST state which claims are architecture-only (DPU offload,
  host-CPU savings, Workload Map ingestion, real GPU isolation).
- **FR-011**: A recorded 8-minute GTC demo script MUST run against the same lab
  environment, covering US1–US4.

### Key Entities

- **Tenant**: one Run:ai Project. It has a namespace, department, queue, GPU quota,
  and priority.
- **BNK Tenant Bundle**: the set of BNK objects derived from one tenant (listener/VIP,
  routes, InferencePool, objectives, rate limit, egress). It is owned by the controller
  and carries labels back to the tenant.
- **Model Deployment**: model server replicas scheduled by the Run:ai Scheduler in a tenant namespace,
  with GPU fractions.
- **Usage Record**: token/request counters keyed by project, department, model, and
  direction.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Create a project, and the tenant serves traffic through BNK within 30
  seconds with no manual BNK config.
- **SC-002**: The Endpoint Picker run shows 30% or better lower p50 TTFT, or a
  correspondingly higher prefix-cache hit rate, than the round-robin baseline in the
  same lab run.
- **SC-003**: Under overload, prod-chat p95 TTFT stays within 1.5× its unloaded value
  while research is preempted or shed.
- **SC-004**: A learner completes the core track (US1–US4) in 60 minutes or less. The
  full track takes 90 minutes or less.
- **SC-005**: Lab start-to-ready (all components healthy) takes 5 minutes or less from
  the prebaked image.
- **SC-006**: `instruqt track test` passes, and every challenge's check script passes
  after its solve script, 3 runs in a row.
- **SC-007**: The GTC demo runs end to end in 8 minutes or less, recorded by 2026-10-19.

## Assumptions

- **No real GPUs** in Instruqt. GPUs come from fake-gpu-operator, which provides
  simulated DCGM metrics. Model servers come from a vLLM-compatible simulator (e.g.
  llm-d-inference-sim) exposing `vllm:*` metrics and KV-cache behaviour.
- The DPU/BlueField claims in Santosh's brief are **out of scope for Instruqt**. They
  are covered by the separate recorded GPU-cluster demo, as allowed by Principle VI.
- BNK license (FAR key + JWT) is available and is injected as an Instruqt secret, as in
  the Lambda POC lab.
- The full Run:ai platform is required. If capacity is short, a dedicated `runai` VM is
  added to the cluster (the user approved new or larger clusters).
- The NVIDIA Workload Map ingestion format is not public. Telemetry stops at
  Prometheus/Grafana.
- The track is a new standalone Instruqt track, not an extension of the BNK AI Factory
  track.
