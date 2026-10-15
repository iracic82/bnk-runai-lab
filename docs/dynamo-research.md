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

## BNK 2.4 and Dynamo

What F5 says publicly (press and product pages, not product docs):

- "BIG-IP Next for Kubernetes now leverages NVIDIA NIM statistics, Dynamo runtime signals, and GPU
  telemetry to make inference-aware routing decisions" and "integration with NVIDIA Dynamo and KV Cache
  Manager". Sources:
  - https://www.businesswire.com/news/home/20260317977850/en/F5-and-NVIDIA-Advance-AI-Factory-Economics-With-New-Capabilities-for-Accelerated-AI-Inference
  - https://www.f5.com/products/big-ip/next/kubernetes-on-nvidia-bluefield-dpu
  - https://www.businesswire.com/news/home/20250611617990/en

❓ Not found in clouddocs: any BNK 2.4 CRD or field that targets Dynamo directly. **So the lab does not
use one.** BNK reaches Dynamo with kinds already verified on BNK 2.4 in this repo
(`docs/verified-names.md`, `controller/internal/bnk/objects.go`):

1. The tenancy controller gives Run:ai project `dynamo` its normal tenant: Gateway `tenant-dynamo`
   (VIP from 172.16.201.110-149, port 8000), HTTPRoute `tenant-route` /v1 → InferencePool `model-pool`,
   F5EPP `model-pool-epp`, rate-limit iRule.
2. The Dynamo frontend pod carries `app.kubernetes.io/part-of=model-server`, so it is the endpoint of
   `model-pool`.
3. `platform/dynamo/route.yaml` adds HTTPRoute `dynamo-route` (gateway.networking.k8s.io/v1) on the same
   Gateway, with longer path prefixes (`/v1/chat/completions`, `/v1/completions`, `/v1/models`) straight
   to Service `dynamo-frontend:8000`.

Either route ends at the Dynamo frontend, which then picks the worker. ⚠️ Open: which of the two routes
BNK prefers (Gateway API says the longest prefix wins), and whether F5EPP (engine `vllm`) scores a
Dynamo frontend cleanly when it lacks `vllm:` metrics. The check script only asserts a 200 through the
VIP, which either path satisfies.

## Risks

- Hugging Face reachability at pod start (tokenizer). Set `HF_TOKEN` in secret `hf-token` if rate
  limited; the manifests read it optionally.
- First pull of the planner image (≈ 0.67 GB compressed) on the gpu node; deploy.sh waits up to 15 min.
- Project parent: deploy.sh uses the first existing `Department` (or `default`).
