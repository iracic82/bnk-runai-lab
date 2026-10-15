# Contract: bnk-runai-tenancy controller

## Watches (read-only)
| GVK | Scope | Why |
|---|---|---|
| `kai.resources/v1alpha1` Project | cluster | tenant source of truth |
| `kai.resources/v1alpha1` Department | cluster | department label |
| `scheduling.run.ai/*` Queue | cluster | priority, allocation |
| `v1` Pod (label `app.kubernetes.io/part-of=model-server`) | tenant namespaces | readiness only (EPP does endpoint picking) |
| BNK bundle kinds | tenant namespaces | drift correction |

## Writes
Only the objects listed in data-model.md "Outputs", always with the `tenancy.f5.com/*`
labels. Never writes upstream Run:ai/KAI objects, except adding and removing its own
finalizer `tenancy.f5.com/bundle` on Projects.

## Configuration (Helm values)
```yaml
baseRps: 20                 # rps per deserved GPU
minGpuFactor: 0.25
criticalPriorityThreshold: 100
hostnameTemplate: "{{project}}.ai.acme.lab"
eppRef: {name: f5-epp, namespace: f5-utils}   # verified in S0
egress: {enabled: true, snatPoolCidr: 10.250.0.0/24}
```

## Per-Project annotation overrides (on the KAI Project)
| Annotation | Effect |
|---|---|
| `tenancy.f5.com/rps` | explicit rate limit |
| `tenancy.f5.com/criticality` | `Critical` / `Standard` / `Sheddable` |
| `tenancy.f5.com/skip` | `"true"` → no bundle |

## RBAC
get/list/watch on Project, Department, Queue, Pod. update on Project (finalizer only).
Full CRUD on BNK bundle kinds and HTTPRoute.

## Guarantees
- Converges within 30 s of any input change (SC-001).
- Idempotent server-side apply. Restart-safe.
- Deletion is complete before the finalizer is removed.
