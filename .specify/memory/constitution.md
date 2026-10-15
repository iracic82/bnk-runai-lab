<!--
Sync Impact Report
- Version change: (template) → 1.0.0
- Principles defined: I Real Software, Simulated Hardware; II Verified Names Only;
  III Tenant Defined Once; IV Every Feature Earns Its Place; V Instruqt-Ready;
  VI Honest Claims
- Added sections: Technical Constraints; Development Workflow & Quality Gates
- Removed sections: none
- Deferred TODOs: none
-->

# BNK 2.4 × Open-Source Run:ai Lab Constitution

## Core Principles

### I. Real Software, Simulated Hardware

The lab MUST run real F5 BIG-IP Next for Kubernetes (BNK) 2.4 in host mode and real
open-source NVIDIA Run:ai components (KAI Scheduler, KAI Resource Management, and the
Run:ai control plane where it fits the VM budget). Only hardware MAY be simulated:
GPUs via fake-gpu-operator (including its DCGM metrics) and model servers via a
vLLM-compatible simulator that exposes real `vllm:*` metrics and OpenAI-compatible APIs.
No BNK or Run:ai behaviour may be mocked, scripted, or faked.

Rationale: the audience is NVIDIA and F5 engineers. One faked control-plane behaviour
discredits the whole demo.

### II. Verified Names Only

Every CRD kind, API group, label, annotation, and Helm value used in code or lab text
MUST be verified against source: the cloned repos `dsx-ai-factory/run-ai` and
`kai-scheduler/*`, or BNK 2.4 clouddocs. Each verified identifier MUST be recorded in
`docs/verified-names.md` with its source path or URL. Unverified names stay out of code.

Rationale: Santosh's brief lists names from the pre-release design. Several have already
changed (for example, `InferenceWorkload` is being migrated to `WorkloadRunner`).

### III. Tenant Defined Once

A tenant is created only in Run:ai (a Project with its Queue). All per-tenant BNK
configuration MUST be derived by the open-source tenancy controller, including gateway
listener, routes, InferencePool, rate limits, egress, and metering labels. Learners MUST
NOT hand-write per-tenant BNK config, except in a challenge whose explicit purpose is to
show what the controller generates.

Rationale: "define once in Run:ai, exists in BNK" is the core message to NVIDIA.

### IV. Every Feature Earns Its Place

Each BNK 2.4 feature shown MUST answer a concrete Run:ai moment: a scheduling,
preemption, quota, fraction, scale, or rollout event. Feature tours with no Run:ai
trigger are out of scope.

Rationale: the demo's value is the composition of the two systems, not either one alone.

### V. Instruqt-Ready by Construction

- The lab MUST run from a prebaked VM image, with lab start under 5 minutes.
- Images MUST be linux/amd64, from public Docker Hub or private GAR.
- Learner commands MUST be cwd-independent and go through a single CLI wrapper (`labctl`).
- Every challenge MUST have setup, check, and solve scripts.
- Secrets MUST NOT be baked into images. The BNK FAR key and JWT are injected as Instruqt
  secrets at runtime.

Rationale: these are lessons learned from the BNK AI Factory track.

### VI. Honest Claims

Lab text MUST label what the environment cannot prove: DPU offload, host-CPU savings,
real GPU memory isolation, and NVIDIA Workload Map ingestion. These are shown as
architecture or a recorded GPU-cluster demo, never as live results.

Rationale: overclaiming in front of NVIDIA costs more than any feature gains.

## Technical Constraints

- Kubernetes: a single-node or multi-node Kind cluster on one Instruqt VM. The node
  version MUST satisfy both the BNK 2.4 support matrix (K8s 1.35) and Run:ai/KAI
  requirements.
- Tenancy controller: Go with controller-runtime, Apache 2.0, in this repo, published
  publicly.
- Observability: Prometheus + Grafana. The metric contract is
  `bnk_tokens_total{project,department,model,direction}` or the request-count fallback.
- No NVIDIA logos or trademark misuse. The wording is "integrates with NVIDIA Run:ai".

## Development Workflow & Quality Gates

1. The spike proves that the VM budget and BNK licensing work before any challenge is built.
2. Spec → plan → tasks via spec-kit. Implementation follows the task list.
3. Each challenge passes its check script against the solve script in CI-like local runs.
4. `instruqt track test` and a full browser walkthrough MUST pass before pushing the track
   to Instruqt.

## Governance

This constitution supersedes ad-hoc decisions for this repo. Amendments require updating
this file with a version bump (semver: MAJOR for principle removal or redefinition, MINOR
for additions, PATCH for wording), plus a note in the Sync Impact Report. Every spec and
plan MUST include a Constitution Check against Principles I–VI.

**Version**: 1.0.0 | **Ratified**: 2026-10-15 | **Last Amended**: 2026-10-15
