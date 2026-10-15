# Phase 0 Research: BNK 2.4 × Open-Source Run:ai Lab

Status of each unknown: ✅ resolved from source · 🔬 resolved by the Spike (hard gate S0)
· ❓ external dependency.

## R1. What the open-source Run:ai release actually ships ✅

- **Decision**: The controller watches **Run:ai Projects/Departments as stored on the cluster**,
  i.e. the `kai.resources/v1alpha1` Project/Department CRDs that Run:ai's cluster chart
  (via its KRM dependency) reconciles, plus Run:ai scheduling Queues (`scheduling.run.ai`).
  Projects are created by the learner in the **Run:ai UI/CLI**. Do **not** depend on
  `InferenceWorkload`.
- **Rationale**: Project and Queue CRDs are verified in source. `InferenceWorkload` is
  being migrated to `WorkloadRunner` (`run.ai/v1alpha1`, which renders a Knative
  Service), so it's a moving target. The Project CRD carries the tenant namespace in
  `status.namespace` (example: project `research` → namespace `kai-research`), not the
  `runai-<project>` convention the brief assumes.
- **Alternatives**: Watching `InferenceWorkload` (rejected: legacy and in migration).
  Watching the full Run:ai control-plane API (rejected: couples the controller to a
  heavy component that may be dropped by the VM budget).
- **Correction to brief**: the namespace is `kai-<project>`, read from status. Project
  queue quota is `resources.gpu.deserved` (the Queue CRD calls it `quota`). The pod
  queue label is `kai.scheduler/queue`.

## R2. GPUs without GPUs ✅

- **Decision**: **fake-gpu-operator** (simulated nodes, A10-class, with DCGM-style
  metrics) and KAI `global.gpuSharingMode=NonMemoryEnforced`, using the
  `gpu-fraction` annotation.
- **Rationale**: This is the same setup Run:ai's own `local-dev.sh` uses. Fractions
  are scheduled for real by KAI; memory isolation can't be enforced without hardware,
  which is disclosed per Principle VI. NvFractions/HAMi need real CUDA.
- **Alternatives**: real GPUs (Instruqt can't provide them; this belongs to a separate
  recorded demo).

## R3. Model servers ✅ (feature-level) / 🔬 (EPP compatibility)

- **Decision**: **llm-d-inference-sim** as the model server.
- **Rationale**: It's OpenAI-compatible and exposes a subset of `vllm:*` metrics. It
  models TTFT/ITL, latency growth under load, KV-cache usage with ZMQ block
  alloc/evict events, P/D disaggregation manifests, LoRA, and failure injection. That
  covers US2 (prefix affinity), US3 (saturation), US5 (two versions), and US7
  (P/D) with no GPU.
- **Spike check**: whether the F5 Endpoint Picker consumes the metrics and KV events the
  sim emits, using the same metric names as real vLLM.
- **Alternatives**: the existing `aifactory-sim` from the BNK lab (fallback; it lacks
  KV-cache events). Bedrock shims as in bnk-forge (real tokens, but needs AWS and adds
  cost and latency noise).

## R4. Cluster: reuse the proven real 3-VM BNK 2.4 cluster ✅ (revised 2026-10-15)

- **Decision**: **No Kind.** Reuse the real three-node kubeadm cluster that the
  `bnk-ai-kv-lab` and Lambda POC tracks already build on Instruqt:
  - Image `f5/bnk-f5`.
  - VMs `bnk` (n2-standard-16, control plane + learner host), `dpu` (n2-standard-8, TMM pod,
    simulated BlueField via the gretap `pcie` tunnel), and `server` (n2-standard-16,
    workloads).
  - K8s **1.30**, Calico, real BNK 2.4.0 via the public `iracic82/bnk-deploy` installer
    (FLO v2.30.0-0.5.2).
  - Team secrets `FAR_PULL_B64`, `BNK_LICENSE_JWT`, `GH_READ_TOKEN`.
  - Run:ai (KAI Scheduler + KAI-RM + fake-gpu-operator) is installed **on top**, with the
    `server` node (and optionally a 4th `gpu` VM) as the fake GPU pool.
- **Rationale**: This cluster runs real BNK 2.4 today, with a working F5EPP, InferencePool,
  NetPolicy+iRule, and SPK egress. The license path is proven. It also gives a
  **separate "DPU" node for TMM**, so we can show the request path from Santosh's diagram
  (ingress work on the DPU node, model pods on the GPU node) honestly as a topology, even
  though the node is simulated.
- **Compatibility**: KAI needs K8s ≥1.33 only for in-place resize, which we don't use.
  KAI-RM uses `matchConditions` (GA 1.30). So **1.30 is OK**, to be confirmed in the spike.
- **Alternatives**: Kind on one VM (rejected: BNK data plane can't run fully on Kind,
  see the bnk-deploy README "e2e-kind cannot install the data plane", and it would
  duplicate a proven install). A GCP/AWS managed cluster (kept as fallback; cloud accounts
  are available on the Instruqt team).

## R5. Full NVIDIA Run:ai, not "KAI only" ✅ (revised 2026-10-15)

- **Decision**: Install the **full open-source NVIDIA Run:ai platform**: control plane
  (UI, API, projects/departments, workloads) **and** the Run:ai cluster chart (`runai-chart`).
  The learner works in the **Run:ai UI and Run:ai CLI/API**, never with raw KAI objects.
- **Rationale**: Run:ai's own architecture doc
  (`run-ai/docs/developer/architecture.md`) states that KAI Resource Management is a direct
  Helm dependency of `runai-chart`, and KAI Scheduler is the engine of the Run:ai
  Scheduler. KAI is Run:ai's internals. The demo and the NVIDIA ask are about **Run:ai**.
  The controller watches the `kai.resources` Project CRDs only because that is where Run:ai
  stores its projects on the cluster. To the learner it is "a Run:ai project".
- **Placement**: The Run:ai control plane (~50 services, Keycloak, Postgres, NATS) needs its
  own capacity. Add a dedicated **`runai` VM** (n2-standard-16, 64 GB) to the cluster,
  rather than squeezing it onto `bnk`. If the spike shows enough headroom, fold it back.
- **Images**: Run:ai images are built from source by `local-dev.sh`; there's no public
  registry yet. Build once (`./local-dev.sh` image build phase) and push linux/amd64 images
  to our **GAR** (Instruqt pulls private GAR). Pin by digest in `platform/versions.env`.
- **Alternatives**: KAI + KRM only (rejected by the user: the lab must show Run:ai itself).
- **Spike check**: Run:ai control plane + cluster chart on K8s 1.30, with BNK as the
  Gateway API controller for the Run:ai control-plane hostname (this also produces the
  docs-PR material, T074).

## R6. BNK 2.4 feature → Run:ai mapping ✅ (design) / 🔬 (CR names)

| Run:ai moment | BNK 2.4 capability | Challenge |
|---|---|---|
| Project created/deleted | Gateway listener/VIP + GatewaySettings + routes (via controller) | C2 |
| Queue GPU quota | Per-tenant rate limit (NetPolicy/SecPolicy) | C2 |
| KAI places replicas on GPU fractions | InferencePool + F5 Endpoint Picker, prefix/KV-cache aware | C3 |
| Queue priority + KAI preemption | InferenceObjective criticality (critical / sheddable) | C4 |
| Tokens per project | Token/request metering exporter (+ Analyzer DCGM metrics if usable) | C5 |
| New model version deployed | InferenceModelRewrite weighted rollout | C6 |
| Project namespace pulls models | EgressGateway with per-namespace SNAT | C7 |
| KAI gang-schedules prefill + decode | P/D-aware routing | C8 (stretch) |

**Spike check**: exact kinds, API versions, and field names in a running BNK 2.4
install, recorded in `docs/verified-names.md`.

## R7. Token metering (brief W2) ❓→🔬

- **Decision**: Two paths, with the spike picking one.
  1. If BNK 2.4 exposes per-request token counts (logs/HSL/OTel), export them as
     `bnk_tokens_total{project,department,model,direction}`.
  2. **Fallback (lab-side, honest)**: request counts per project from BNK stats, plus
     token counts from the model servers' `vllm:prompt_tokens_total` /
     `vllm:generation_tokens_total`, attributed to the project by namespace. Labelled
     "measured at the model server, not the gateway".
- **Rationale**: This never blocks the demo and never overclaims.
- **Owner of answer**: BNK PM (brief task, due 2026-10-15).

## R8. Licensing in Instruqt ✅

- **Decision**: Reuse the existing f5 team secrets `FAR_PULL_B64` and `BNK_LICENSE_JWT`
  (same as the Lambda POC and bnk-ai-kv-lab), consumed by bnk-deploy `lib/60-license.sh`.
  They are never baked into images or the repo.
- **Rationale**: Already proven on live Instruqt sandboxes.

## R11. Verified BNK 2.4 object names (from the working bnk-ai-kv-lab manifests) ✅

| Purpose | Kind / apiVersion |
|---|---|
| GatewayClass controller | `controllerName: f5.com/f5-bnk-f5-cne-controller` |
| Gateway / HTTPRoute | `gateway.networking.k8s.io/v1` (Gateway in `f5-bnk`, routes in tenant ns, `allowedRoutes.from: All`) |
| Endpoint Picker | `F5EPP` `inference.k8s.f5.com/v1alpha1` (spec.poolRef, engine `vllm`, eppConfig schedulingProfiles prefix-cache/queue-scorer); **must live in the same namespace as the pool and pods** |
| InferencePool | `inference.networking.k8s.io/v1` (selector.matchLabels, targetPorts, endpointPickerRef port 9002, failureMode) |
| Policy attach | `NetPolicy` `gateway.k8s.f5.com/v1alpha1` → `F5BigCneIrule` `k8s.f5net.com` (iRule-based rate limit / metering) |
| Egress | `F5SPKEgress` `k8s.f5net.com/v3` + `F5SPKSnatpool` `k8s.f5net.com/v1` (pseudoCNIConfig.namespaces) |
| EPP namespaces | set at install time via `BNK_EPP_NAMESPACES` (a post-install patch rolls the controller), so **tenant namespaces must be known before BNK install** |

**Design consequences**:
1. The controller creates an F5EPP **per tenant namespace**, and the tenant namespaces
   must be in the EPP namespace list. So either pre-create the KAI Projects (`kai-prod-chat`,
   `kai-research`) before the BNK install, or confirm in the spike that adding namespaces
   later works without disruption.
2. Rate limiting and token metering go through an **iRule** (F5BigCneIrule + NetPolicy),
   the same mechanism the Lambda lab uses for per-user metering and quotas. This largely
   answers R7: token counting at the gateway is possible via iRule, as Lambda's challenge 4
   does it.
3. InferenceObjective / InferenceModelRewrite are not used in existing labs. They're still
   to be verified in the spike.

## R9. Instruqt packaging ✅ (from BNK lab lessons)

- **Decision**: Custom GCP image (Packer) with Kind, images preloaded into a local
  registry, and Helm charts vendored. Challenge-0 setup only starts the cluster and
  applies the license.
- Terminal conventions: `labctl` on PATH, cwd set via `/etc/profile.d`, about 30
  seconds for the terminal to attach, Grafana panels full-width, and load runs of about
  30 seconds.

## R10. GTC Berlin (20–22 Oct) vs Instruqt track

- **Decision**: Two deliverables from one codebase.
  - **D1 — GTC demo (by 2026-10-19)**: US1–US4 on the spike VM, recorded, plus a live
    fallback. If Santosh's partner cluster with BlueField-3 is available, the same
    controller and chart run there for the DPU and host-CPU slide.
  - **D2 — Instruqt track (after GTC)**: all challenges, validated.
- **Rationale**: Four working days to GTC. The controller and chart are shared, so
  nothing is thrown away.
