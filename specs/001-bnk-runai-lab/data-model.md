# Data Model

All state lives in the Kubernetes API. The upstream objects are read-only inputs; the
controller-owned objects form the "BNK Tenant Bundle".

## Inputs (watched, never written)

### Project — `kai.resources/v1alpha1`, cluster-scoped (verified)
| Field | Use |
|---|---|
| `metadata.name` | Tenant ID (`prod-chat`, `research`) |
| `spec.parent` | Department name → `department` label |
| `spec.queues[].resources.gpu.{deserved,limit,overQuotaWeight}` | Rate-limit derivation |
| `spec.queues[].priority` | Objective criticality mapping |
| `status.namespace` | Tenant namespace (e.g. `kai-research`). Authoritative; never derived from the name |
| `status.conditions` | Reconcile only when the Project is Ready |

### Queue — `scheduling.run.ai`, cluster-scoped (verified)
Read for live `status.allocated` (shown next to metering) and `spec.priority`.

### Department — `kai.resources/v1alpha1` (verified)
Read for the display name and (stretch) quota inheritance.

### Model Pods (core/v1)
Selected by namespace = `status.namespace` **and** label `app.kubernetes.io/part-of=model-server`.
KAI adds `kai.scheduler/queue`, which is shown and used to correlate metrics.

## Outputs: BNK Tenant Bundle (owned, labelled, garbage-collected)

Every object carries:
```
labels:
  tenancy.f5.com/project: <project>
  tenancy.f5.com/department: <department>
  app.kubernetes.io/managed-by: bnk-runai-tenancy
annotations:
  tenancy.f5.com/source: kai.resources/v1alpha1/Project/<project>
```
Ownership is tracked by these labels plus a finalizer on the Project, rather than
ownerReferences, because the Project is cluster-scoped and the bundle is namespaced.

| Object (BNK 2.4, kinds verified in spike S0) | Derived from | Rule |
|---|---|---|
| Gateway listener / GatewaySettings | Project | One hostname per tenant: `<project>.ai.acme.lab` |
| HTTPRoute | Project | Routes `/v1/*` → InferencePool |
| InferencePool | Model pods in tenant namespace | selector `part-of=model-server`; endpointPickerRef → F5 EPP |
| InferenceObjective | Queue priority | priority ≥ 100 → `Critical`; < 100 → `Sheddable` (thresholds in values) |
| Rate-limit policy (NetPolicy/SecPolicy) | GPU deserved | `rps = base_rps × max(deserved, 0.25)`; override annotation `tenancy.f5.com/rps` |
| EgressGateway | Tenant namespace | NamespaceSelector = tenant ns; SNAT from per-tenant pool |
| InferenceModelRewrite | (learner-applied in C6) | Not controller-owned; it's the one hand-written object, on purpose |

## State Transitions (per Project)

```
Absent ──create Project──▶ Pending (Project not Ready / no namespace)
Pending ──Project Ready──▶ Provisioning ──all bundle objects applied──▶ Serving
Serving ──quota/priority change──▶ Provisioning ──▶ Serving
Serving ──bundle object deleted out-of-band──▶ Provisioning (drift fix) ──▶ Serving
any ──Project deleting──▶ Draining (finalizer: delete bundle) ──▶ Absent
any ──namespace conflict──▶ Error (condition set, no partial bundle)
```

Status is written as Kubernetes Events on the Project and as the gauge
`bnk_tenant_state{project,state}`. The controller does not write the Project's
status, which is owned upstream.

## Validation Rules

- Project without a GPU queue gets `rps = base_rps × 0.25` (FR edge case: never unlimited).
- Two Projects resolving to one namespace → Error state for both, no writes.
- Reconcile is idempotent: server-side apply with field manager `bnk-runai-tenancy`.
