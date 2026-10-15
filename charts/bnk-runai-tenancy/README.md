# bnk-runai-tenancy

One F5 BIG-IP Next for Kubernetes (BNK) tenant per NVIDIA Run:ai project.

The controller watches Run:ai projects (`kai.resources`) and, for each one, renders a BNK tenant: a
Gateway on its own VIP, an HTTPRoute and InferencePool in the project namespace, an F5 Endpoint Picker,
a rate-limit iRule sized from the project's GPU quota, and a NetPolicy. Deleting the project removes the
tenant. The optional meter collects per-project token usage from the BNK front door and keeps totals per
project and department.

The templates reproduce `controller/config/deploy.yaml` and `controller/config/meter.yaml`, the manifests
verified live on BNK 2.4 with open-source Run:ai.

## Prerequisites

- **F5 BNK 2.4** with a GatewayClass `f5-gateway-class` whose `controllerName` is
  `f5.com/f5-bnk-f5-cne-controller`.
- **BNK installed with `eppNamespaces` covering the Run:ai project namespaces** (e.g. `runai-*`), so the
  per-tenant Endpoint Pickers are served.
- **Open-source NVIDIA Run:ai cluster** with the `kai.resources` CRDs (projects, departments).
- Gateway API Inference Extension CRDs (InferencePool), as shipped with BNK 2.4.
- **Prometheus Operator CRDs** (optional): only needed with `monitoring.enabled=true`.
- Helm 3.8+ (or Helm 4).

## Install

```bash
helm install bnk-runai-tenancy charts/bnk-runai-tenancy -n bnk-runai-tenancy --create-namespace
```

The chart does not template a Namespace; it installs into the release namespace. Roles that must live in
the BNK namespace (VIP ConfigMap, pull secret, the meter's Gateway list) are created in `bnk.namespace`,
bound to the service accounts in the release namespace. Install once per cluster: the ClusterRoles and the
ConfigMaps the binaries own (`bnk-runai-tenancy-vips`, `bnk-runai-usage`) have fixed names.

Create projects through the Run:ai API or UI. Run:ai's admission webhook forbids `kubectl` writes to
projects and departments. Then check the tenants:

```bash
kubectl get gateways -n f5-bnk -l tenancy.f5.com/project
```

Without metering:

```bash
helm install bnk-runai-tenancy charts/bnk-runai-tenancy -n bnk-runai-tenancy --create-namespace \
  --set metering.enabled=false
```

## Values

| Key | Default | Description |
|---|---|---|
| `controller.image.repository` | `docker.io/iracic82/bnk-runai-tenancy` | Controller image |
| `controller.image.tag` | `0.1.6` | Controller image tag |
| `controller.image.pullPolicy` | `Always` | Controller pull policy |
| `controller.replicaCount` | `1` | Replicas (one active via leader election) |
| `controller.leaderElect` | `true` | `--leader-elect` |
| `controller.nodeSelector` | `{app: runai-control-plane}` | Controller placement |
| `controller.tolerations` / `affinity` | `[]` / `{}` | Controller scheduling |
| `controller.resources` | req `50m`/`64Mi`, limit `256Mi` | Controller resources |
| `controller.extraArgs` | `[]` | Extra args, e.g. `--dry-run` |
| `bnk.namespace` | `f5-bnk` | `--bnk-namespace`; BNK-side Roles go here |
| `bnk.gatewayClass` | `f5-gateway-class` | `--gateway-class` |
| `bnk.tokenizer` | `NousResearch/Meta-Llama-3.1-8B-Instruct` | `--tokenizer` for the Endpoint Picker prefix-cache scorer |
| `bnk.pullSecret` | `far-pull-secret` | `--pull-secret` copied into tenant namespaces; empty disables copying and all secret RBAC |
| `vipRange` | `172.16.201.110-172.16.201.149` | `--vip-range` for tenant Gateways |
| `policy.baseRPS` | `20` | `--base-rps`, requests/s per deserved GPU |
| `policy.minGPUFactor` | `0.25` | `--min-gpu-factor`, GPU floor for projects without a GPU quota |
| `policy.criticalPriorityThreshold` | `100` | `--critical-priority-threshold` |
| `metering.enabled` | `true` | Deploy the meter and pass metering flags to the controller |
| `metering.sink` | `10.1.10.11:5514` | Controller `--metering-sink`: control-node IP on TMM's network |
| `metering.allowedPeers` | `10.1.10.201` | Meter `--allowed-peers` (TMM self-IPs); empty rejects all |
| `metering.flushInterval` | `5s` | Meter `--flush-interval` |
| `metering.writebackInterval` | `30s` | Meter `--writeback-interval` |
| `metering.token.existingSecret` | `""` | Secret with key `token`; empty generates `bnk-metering-token` (random 32 alnum, stable across upgrades) |
| `meter.image.repository` | `docker.io/iracic82/bnk-runai-meter` | Meter image |
| `meter.image.tag` | `0.1.1` | Meter image tag |
| `meter.image.pullPolicy` | `IfNotPresent` | Meter pull policy |
| `meter.usagePort` / `metricsPort` | `5514` / `9102` | Host ports (meter runs with `hostNetwork`) |
| `meter.nodeSelector` | `{node-role.kubernetes.io/control-plane: ""}` | Meter placement |
| `meter.tolerations` | control-plane and master `NoSchedule` | Meter tolerations |
| `meter.resources` | req `20m`/`32Mi`, limit `128Mi` | Meter resources |
| `meter.extraArgs` | `[]` | Extra meter args |
| `monitoring.enabled` | `false` | ServiceMonitor (controller) and PodMonitor (meter) |
| `monitoring.interval` | `5s` | Scrape interval |

### Metering notes

The iRules send usage over TCP from TMM to `metering.sink`, so the meter runs with `hostNetwork` on a
control-plane node. Before enabling it on a new site, confirm the TMM self-IP reaches `<sink>` and the
control node reaches the tenant VIPs on `:8000`. The token is readable by anyone who can read
F5BigCneIrule objects in the BNK namespace; RBAC on that namespace is the real boundary.

## Validation matrix

| Component | Version | Status |
|---|---|---|
| Kubernetes | 1.30 | verified |
| F5 BNK | 2.4.0 (FLO v2.30.0-0.5.2) | verified |
| NVIDIA Run:ai open source | @ 59e288d7 (cluster chart 0.0.0-dev) | verified |
| kai-resource-management-api | v0.1.5 | verified |
| Gateway API Inference Extension CRDs | v1.4.0 | verified |
| fake-gpu-operator | 0.2.1 | verified (lab) |
| NVIDIA Dynamo | 1.5.1 (Mocker) | verified (lab) |
| Real GPUs / BlueField-3 DPU | - | not yet verified |

### What was verified live

- Tenant from a Run:ai project: a BNK Gateway is created and Programmed.
- Quota to rate limit: a 0.5 GPU project gets 10 req/s; excess requests get 429.
- Cross-tenant route refused: a route to another tenant's Gateway is rejected (`NotAllowedByListeners`).
- Token metering from TMM to the meter, with token auth and the peer allowlist.
- Deleting the project removes its BNK objects.
