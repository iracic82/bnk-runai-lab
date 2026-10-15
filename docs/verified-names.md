# Verified Names

Identifiers checked against source on 2026-10-15. Clones:
`dsx-ai-factory/run-ai@59e288d7`, plus `kai-scheduler/KAI-Scheduler`,
`kai-scheduler/kai-resource-management`, and `kai-scheduler/kai-gpu-fractioning`
(default branch, shallow).

## Run:ai / KAI

| Identifier | Status | Source | Note |
|---|---|---|---|
| `Queue` — `scheduling.run.ai`, cluster-scoped | ✅ verified | `KAI-Scheduler/deployments/kai-scheduler/crds/scheduling.run.ai_queues.yaml` | spec: `displayName`, `parentQueue`, `priority`, `resources.{cpu,gpu,memory}`, `preemptMinRuntime`, `reclaimMinRuntime`; status: `allocated`, `childQueues` |
| `PodGroup` — `scheduling.run.ai` | ✅ verified | `KAI-Scheduler/.../scheduling.run.ai_podgroups.yaml` | gang scheduling unit |
| `Project` — `kai.resources/v1alpha1`, cluster-scoped | ✅ verified | `kai-resource-management/.../crds/kai.resources_projects.yaml` | spec: `namespace`, `parent`, `queues[{name,nodepool,priority,resources}]`, `defaultNodePools`, `enforceKaiScheduler`, `deletionType` |
| `Department` — `kai.resources/v1alpha1`, cluster-scoped | ✅ verified | `kai-resource-management/.../crds/kai.resources_departments.yaml` | spec: `queues[]` |
| `NodePool` — `kai.resources` | ✅ verified | `kai-resource-management/.../crds/kai.resources_nodepools.yaml` | |
| Pod queue label `kai.scheduler/queue` | ✅ verified | `KAI-Scheduler/docs` (40 refs) | legacy `runai/queue` still appears (74 refs in run-ai) |
| Labels `run.ai/project`, `run.ai/department` | ⚠️ present | run-ai monorepo | confirm which component stamps them on namespaces |
| Namespace convention `runai-<project>` | ⚠️ documented | run-ai `docs/` | the Project CRD carries an explicit `spec.namespace`; read that, don't assume the prefix |
| GPU sharing annotations `gpu-fraction`, `gpu-memory` | ✅ verified | `KAI-Scheduler/docs/gpu-sharing` | |
| `InferenceWorkload` | ⚠️ legacy | `run-ai/designs/workloads/infw-to-workloadrunner/` | being migrated to `WorkloadRunner` (`run.ai/v1alpha1`) rendering a **Knative Service** |
| `WorkloadRunner` — `run.ai/v1alpha1` | ✅ in design docs | same | template + plugin instructions; networking via `instructions.networking.*` |
| Run:ai cluster CRDs (`run.ai_clusters`, `kartas`, `restartrequest`, `runaiconfig`) | ✅ verified | `run-ai/runai-cluster/cluster/runai-crd-upgrader/crds/` | |

**Consequence for the controller**: watch `kai.resources/Project` (source of tenant and
namespace) and `scheduling.run.ai/Queue` (quota and priority). Don't depend on
`InferenceWorkload`. Select backends by namespace plus `kai.scheduler/queue`.

## BNK 2.4

To verify against clouddocs (latest = 2.4.0) during the spike. Names are known from
prior research but **not yet confirmed in a running install**:

| Identifier | Status |
|---|---|
| `GatewaySettings`, `Infra`, `EgressGateway`, `SecPolicy`, `NetPolicy` (group `gateway.k8s.f5.com`) | 🔲 confirm in install |
| Gateway API Inference Extension: `InferencePool`, `InferenceObjective`, `InferenceModelRewrite` | 🔲 confirm API versions shipped |
| F5 Endpoint Picker (`endpointPickerRef`, `eppNamespaces`) | 🔲 confirm |
| `CNEInstance` `intelligentLB.enabled`, `F5BigAnalyzer` | 🔲 confirm still present in 2.4 |
| Per-request token counting in BNK 2.4 | ❓ open question (brief W2) |
