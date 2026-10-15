# NVIDIA Dynamo on Run:ai behind BNK: research notes

Checked on 2026-10-15 against `ai-dynamo/dynamo` tag **v1.5.1** (latest non-dev release, 2026-10-07)
and the nvcr.io registry. Status: ✅ verified in source/registry, ⚠️ verified upstream but not yet run
in this lab, ❓ not verifiable.

## What we run

| Piece | Choice | Status |
|---|---|---|
| Image (frontend and Mocker) | `nvcr.io/nvidia/ai-dynamo/dynamo-planner:1.5.1` | ✅ anonymous pull works (nvcr.io `proxy_auth` token, no NGC key); multi-arch index with `linux/amd64`; amd64 layers ≈ 0.67 GB compressed |
| Why the planner image | Upstream mocker manifests say: "There is no dedicated `mocker-runtime` image. The published `dynamo-planner` image includes the ai-dynamo wheel, ai-dynamo-runtime ..." | ✅ `examples/backends/mocker/deploy/agg.yaml` |
| Image facts | entrypoint `/usr/bin/shelless_ulimit`, user `dynamo`, workdir `/workspace`, venv `/opt/dynamo/venv` on PATH, ships `etcd` and `nats-server` binaries | ✅ image config blob |
| Alternative frontend image | `nvcr.io/nvidia/ai-dynamo/dynamo-frontend:1.5.1` exists but is ≈ 3.9 GB; not needed | ✅ registry |
| etcd | `quay.io/coreos/etcd:v3.5.18` | ✅ tag exists (multi-arch). Upstream Helm chart uses `bitnamilegacy/etcd:3.5.18-debian-12-r5`, dev compose uses `bitnamilegacy/etcd:3.6.1` |
| NATS | `nats:2.11.4` with JetStream (`-js`) | ✅ same tag as upstream `dev/docker-compose.yml`, which enables `jetstream: {}` |
| Tokenizer | `Qwen/Qwen3-0.6B` (used by the upstream Mocker K8s tutorial) | ✅ docs; needs Hugging Face access at pod start (tokenizer files only) |

## Mocker

- Launch: `python3 -m dynamo.mocker` (`components/src/dynamo/mocker/`). It "registers workers,
  publishes KV events, and returns mock responses while exercising the same frontend and routing path
  as a model-serving deployment" and "does not request GPU resources".
  Source: `docs/fern/pages/kubernetes/operations/simulation-with-dynosim/mocker-live-simulation.mdx`.
- Flags used (all in `components/src/dynamo/mocker/args.py` @ v1.5.1 and
  `docs/fern/pages/reference/components/mocker-cli-reference.mdx`):
  `--model-path` (HF id or local path, tokenizer only), `--model-name`, `--speedup-ratio` (default 1.0),
  `--num-gpu-blocks-override`, `--block-size` (vLLM default 64), `--enable-prefix-caching` (default on),
  `--disaggregation-mode {agg,prefill,decode}`, `--bootstrap-ports` (prefill), `--engine-type`
  (default `vllm`), `--discovery-backend {kubernetes,etcd,file,mem}` (default env
  `DYN_DISCOVERY_BACKEND` or `etcd`), `--request-plane {nats,tcp}` (default `tcp`),
  `--event-plane {nats,zmq}` (auto: `nats` for etcd/kubernetes).
- Model download: `main.py` calls `fetch_model(model_path, ignore_weights=True)`, so only tokenizer
  and config files are pulled from Hugging Face. ✅

## Frontend

- `python3 -m dynamo.frontend --http-port 8000` (OpenAI-compatible; `frontend_args.py`).
- `--router-mode` choices: `round-robin` (frontend default), `random`, `power-of-two`, `kv`, `direct`,
  `least-loaded`, `device-aware-weighted` (`common/configuration/groups/router_args.py`). We use `kv`.
- Health: `/health` and `/live` on the HTTP port (`lib/llm/src/http/service/health.rs`).

## Discovery and infra

- `--discovery-backend kubernetes` exists, but it watches `DynamoWorkerMetadata`
  (`nvidia.com/v1alpha1`) CRs and EndpointSlices labelled
  `nvidia.com/dynamo-discovery-backend=kubernetes` (`lib/runtime/src/discovery/kube/*.rs`). That CRD
  comes with the Dynamo operator/platform chart. ✅
- Without the operator, the documented path is etcd for discovery and NATS for the event plane.
  The operator itself injects `NATS_SERVER` and `ETCD_ENDPOINTS` into pods
  (`deploy/operator/internal/dynamo/graph.go`, `AddStandardEnvVars`). ✅
- TCP request plane advertises `DYN_TCP_RPC_HOST` or the resolved local IP
  (`lib/runtime/src/utils/ip_resolver.rs`). We set it to the pod IP. ✅
- **Decision**: no operator, no `DynamoGraphDeployment`. Plain Deployments + Services, one etcd and one
  NATS pod in the project namespace. Kept the workers Run:ai-scheduled (`schedulerName: kai-scheduler`,
  `kai.scheduler/queue`, `gpu-fraction`). ⚠️ not yet run on the lab cluster.

## Routing headers and response metadata (for pinning and showing the router)

From `lib/llm/src/protocols/common/extensions.rs` and
`docs/fern/pages/developer-guide/additional-resources/nvidia-request-extensions-nvext.md`: ✅

- `x-dynamo-worker-instance-id` → `backend_instance_id` and `decode_worker_id`
- `x-dynamo-prefill-instance-id` → `prefill_worker_id`
- `x-tenant-id` → `cache_salt` (KV routing namespace, "intended for gateway-controlled tenant identity")
- Unprefixed `x-worker-instance-id` etc. are deprecated aliases.
- Request `"nvext": {"extra_fields": ["worker_id"]}` returns `nvext.worker_id.{prefill_worker_id,
  decode_worker_id,...}` in the response.

## BNK 2.4 and Dynamo: proven live (2026-10-15)

BNK 2.4 documents a Dynamo integration in the F5 Endpoint Picker: `F5EPP spec.engine: dynamo` plus
`spec.natsURL` (clouddocs, "Configure the Inference Data Path"). It works with Dynamo 1.5.1 when Dynamo
is deployed the way NVIDIA documents it. Live result through the BNK VIP: two different prompt
prefixes, six requests each, every one HTTP 200; prefix A always on worker 2609867643287794, prefix B
always on worker 6381314717850254; the picker received Dynamo's kv-events and kv_metrics over NATS.

What it takes (each item was a failure before it was in place):

| Requirement | Why |
|---|---|
| Dynamo operator (`dynamo-platform` 1.5.1 Helm chart, `global.nats.install=true`) and a `DynamoGraphDeployment` | The picker maps a pod to a Dynamo worker ID by hashing the pod name (`hash_pod_name`, dynamo `lib/runtime/src/discovery/kube/utils.rs`). Only Kubernetes discovery (the operator's default) registers workers under that ID. With `DYN_DISCOVERY_BACKEND=etcd` workers get random lease IDs and every request fails with `nvext.decode_worker_id=<n> does not identify a known worker`. |
| A frontend sidecar per worker in direct mode (`frontendSidecar`, `dynamo.frontend --router-mode direct`) | Dynamo's own Gateway API guide. The picker writes the chosen worker into `nvext.decode_worker_id`; the sidecar serves exactly that worker. Without an ID it returns 400 `worker ID required ... Direct routing mode`. |
| InferencePool selecting the operator's labels (`nvidia.com/dynamo-graph-deployment-name`, `nvidia.com/dynamo-component-type: decode`), target port 8000 | Only workers, never the frontend. |
| F5EPP `engine: dynamo`, `natsURL` = the platform NATS, `server.modelServerMetricsPort: 9090` | Operator sets `DYN_SYSTEM_PORT=9090`; Mocker exports `vllm:*` series there after first traffic. |
| F5EPP `eppConfig.config.plugins` override of `dynamo-wire` topics: `component.backend.endpoint.generate.kv-events` / `...kv_metrics` | BNK 2.4's default subscribes to `namespace.*.component.backend.kv-events`; Dynamo 1.5.1 publishes per endpoint (`namespace.<ns>.component.backend.endpoint.generate.kv-events`). Without the override routing still works but without KV events (`received=0`). Documented F5EPP merge override; question for F5 whether the default will change. |
| Node `fs.inotify.max_user_instances` >= 1024 | The platform NATS config reloader crash-loops with `too many open files` on Ubuntu's 128. |

Other facts from the live session:

- The release note says `x-worker-id`; the picker actually uses `nvext.decode_worker_id`. Dynamo ignores
  an `x-worker-id` header (it accepts `x-dynamo-worker-instance-id` / `x-worker-instance-id`).
- Dynamo's own router (`--router-mode kv` frontend) is not in the request path in this mode: BNK picks the
  worker.
- Kept Run:ai in charge: the DGD pod templates set `schedulerName: runai-scheduler`, label
  `runai/queue: dynamo` and annotation `gpu-fraction: "0.5"`; the operator passes them through.

## Risks

- Hugging Face reachability at pod start (tokenizer). Set `HF_TOKEN` in secret `hf-token` if rate
  limited; the manifests read it optionally.
- First pull of the planner image (≈ 0.67 GB compressed) on the gpu node; deploy.sh waits up to 15 min.
- Project parent: deploy.sh uses the first existing `Department` (or `default`).

## NATS access (shared event plane)

Dynamo's NATS (dynamo-platform chart, `global.nats.install=true`) has no authentication, and every Dynamo
tenant publishes its KV-cache events there. A pod that can reach it could read other tenants' prompt-block
hashes or publish fake events that steer BNK's Endpoint Picker. `platform/dynamo/nats-policy.yaml` limits
port 4222 to namespaces labelled `tenancy.f5.com/engine=dynamo` (the workers and the BNK picker, which the
controller renders into the tenant namespace) and to `dynamo-system`. Tenants still share one NATS: a
labelled tenant can see another Dynamo tenant's subjects. Per-tenant NATS accounts would close that and are
not done in this lab.
