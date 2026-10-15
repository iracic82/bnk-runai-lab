# Contract: labctl (learner + check-script CLI)

`labctl/labctl`, a single bash script (jq, kubectl, curl; python3 for `load`). Installed on the control
node as `/usr/local/bin/labctl`. Commands are cwd-independent, exit 0 on success and non-zero on
failure, and with `--json` print one JSON document on stdout (progress goes to stderr).

## Where things come from

- **Run:ai writes** go through the Run:ai REST API at `https://runai.lab` (admin login from
  `RUNAI_ADMIN_EMAIL`/`RUNAI_ADMIN_PASSWORD`, defaults from `platform/runai/install.sh`). Run:ai's
  admission webhook refuses `kubectl` writes to `kai.resources` Projects/Departments. TLS is verified
  against the lab CA `RUNAI_CA_FILE` (default `/etc/runai/ca.crt`, installed by
  `platform/runai/install.sh`); there is no `-k` fallback.
  - token `POST /api/v1/token` `{"grantType":"password",...}` -> `.accessToken`
  - cluster `GET /api/v1/clusters` (name `acme-ai-factory`), node pool `GET /api/v1/node-pools` (`default`)
  - department `GET|POST /api/v1/org-unit/departments`, quota `PATCH .../departments/{id}/resources`
  - project `POST /api/v1/org-unit/projects`, quota/rank `PATCH .../projects/{id}/resources`,
    `DELETE .../projects/{id}`
- **Reads** use kubectl: the `kai.resources/v1alpha1` Project (what the tenancy controller reads), the
  BNK objects labelled `tenancy.f5.com/project=<project>`, the Gateway status address (VIP), the iRule
  (rate limit), project Events, and ConfigMap `bnk-runai-tenancy/bnk-runai-usage` (metered tokens).

## Commands

| Command | Purpose |
|---|---|
| `labctl status [--json]` | nodes, BNK TMM + data path (10.1.10.201), Run:ai pods + API, fake GPUs, tenancy controller, meter, model servers, Grafana |
| `labctl tenant create <name> --gpu <n> [--priority <p>] [--department <d>] [--no-wait] [--json]` | create **or update** the Run:ai project via the API; creates the department if missing; waits until the kai.resources Project is `Ready`, has `status.namespace` and shows the new quota |
| `labctl tenant delete <name> [--no-wait]` | delete the Run:ai project via the API; waits for the kai.resources Project to disappear |
| `labctl tenant show <name> [--json]` | Run:ai side (department, namespace, phase, quota, queue priority, allocated) beside the BNK side (VIP, iRule rate limit, last controller event, metered tokens, every labelled BNK object) |
| `labctl tenant list [--json]` | one line per project: department, namespace, phase, GPU, priority, VIP, req/s limit |
| `labctl model deploy <tenant> [<model>] [--replicas N] [--fraction F] [--name N] [--json]` | vllm-twin model server in the project namespace, scheduled by Run:ai (defaults: model `NousResearch/Meta-Llama-3.1-8B-Instruct`, 1 replica, fraction 0.5, name `model`); waits for rollout |
| `labctl model delete <tenant> [--name N]` | remove it |
| `labctl vip <tenant>` | print the tenant VIP only (Gateway `f5-bnk/tenant-<tenant>` `.status.addresses[0]`) |
| `labctl chat <tenant> "<prompt>" [--max-tokens N] [--json]` | one `/v1/chat/completions` at `http://<VIP>:8000`; prints HTTP status, `X-BNK-Tenant` header, answer and usage; exit 0 only on 200 |
| `labctl load steady\|overload\|prefix-heavy [--tenant T] [--seconds S] [--rps R] [--json]` | open-loop load via `loadgen/loadgen.py`; per-second 200/429/other and p50/p95 |
| `labctl dash [panel]` | Grafana URL for the Instruqt tab (`control:3000`, dashboard uid `one-tenant-two-planes`) |
| `labctl verify <challenge>` | runs `instruqt/*/<challenge>*/check-control` (same logic as the Instruqt check) |

`labctl routing rr|epp` is not offered: the tenancy controller owns each tenant's InferencePool and
always wires the F5 Endpoint Picker.

## Semantics

- **Priority.** Run:ai's API takes a scheduling *rank*, not a number. `--priority` accepts a rank name
  (`Highest`, `VeryHigh`, `High`, `MediumHigh`, `Medium`, `MediumLow`, `Low`, `VeryLow`, `Lowest`) or a
  number, mapped to the highest rank whose value is at or below it (255, 240, 210, 180, 150, 100, 50,
  20, 1; source: run-ai `control-plane/org-unit-service` `RankValuesMap`). Run:ai writes that value as
  the queue `priority` on the kai.resources Project. So `--priority 150` = `Medium` = 150 (Critical at
  the controller's threshold 100), `--priority 50` = `Low` = 50 (Sheddable). Omitted: Run:ai's default
  `MediumLow` (100).
- **Department.** Defaults to Run:ai's `default` department (override `LABCTL_DEPARTMENT`). A missing
  department is created with GPU quota = the cluster's allocatable GPUs; an existing one is raised to
  that if lower, so a project quota is never refused for exceeding its department. Run:ai cannot move a
  project between departments; `--department` on an existing project is reported and ignored.
- **Namespace.** Requested as `runai-<name>`; labctl always reads the real one from
  `status.namespace`.
- **Model pods.** Labels `app.kubernetes.io/part-of=model-server` (what the BNK InferencePool selects),
  `app.kubernetes.io/name=<name>`, `tenancy.f5.com/project=<tenant>`, `kai.scheduler/queue=<queue>`
  (plus Run:ai's queue label key, `runai/queue`, read from `configs.kai.scheduler/kai-config`).
  `schedulerName` is the one in kai-config (`runai-scheduler` under Run:ai; `kai-scheduler` default).
  `--fraction` in (0,1) sets annotation `gpu-fraction: "<F>"` and requests **no** `nvidia.com/gpu`
  (KAI admission rejects combining them); a whole number requests `nvidia.com/gpu: <F>`; `0` = no GPU.
  Image `MODEL_SIM_IMAGE` (`ghcr.io/iracic82/vllm-twin:0.2.0`), port 8000, `/metrics` in vLLM names,
  `nodeSelector app=workload` (the gpu node).
- **Data path.** VIPs `172.16.201.110-149:8000` are reached from the control node (and the gpu node)
  through the route `172.16.201.0/24 via 10.1.10.201 dev br1` that `platform/cluster/setup-cluster.sh`
  installs, so `chat` and `load` run on the control node directly. Other nodes and pods cannot reach
  the VIPs.

## Load profiles (`loadgen/loadgen.py`, python3 stdlib)

| Profile | Rate | Payload | Expectation |
|---|---|---|---|
| `steady` | 8 req/s | short prompt, `max_tokens` 32 | under any tenant's limit (0.5 GPU = 10 req/s), all 200 |
| `overload` | 40 req/s | short prompt, `max_tokens` 32 | a 0.5-GPU tenant gets ~10/s 200 and the rest 429 from BNK |
| `prefix-heavy` | 6 req/s | 4 shared ~1.1k-word system prompts + short question, `max_tokens` 16 | prefix-cache hit rate rises; the Endpoint Picker keeps a prefix on one replica |

Open loop: requests leave at the target rate whatever the server does. One line per second
(`200=`, `429=`, `other=`, `err=`, `p50`, `p95` of the 200s), then a summary with the
`X-BNK-Tenant` header values and token usage. `--json` prints the summary (with `per_second`) on stdout.

## Environment

`KUBECONFIG` (`/etc/kubernetes/admin.conf`), `LAB_ROOT`, `RUNAI_URL`, `RUNAI_CA_FILE`,
`RUNAI_ADMIN_EMAIL`, `RUNAI_ADMIN_PASSWORD`, `RUNAI_CLUSTER_NAME`, `RUNAI_NODEPOOL`, `LABCTL_DEPARTMENT`,
`MODEL_SIM_IMAGE`, `SIM_MODEL`, `BNK_NS` (`f5-bnk`), `TENANCY_NS` (`bnk-runai-tenancy`), `VIP_PORT`
(8000), `GRAFANA_PORT` (3000).
