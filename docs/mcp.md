# Agents drive the AI factory over MCP

Stretch item C of the NVIDIA demo. An AI agent asks for a tenant. It creates a Run:ai project through
NVIDIA's open-source Run:ai MCP server, Run:ai turns that into a `kai.resources` Project, our tenancy
controller builds the BNK tenant, and the agent reads the tenant's front door back through a second MCP
server we own. Nobody runs kubectl and nobody touches BNK by hand.

```
agent ──MCP──▶ runai-mcp-server (NVIDIA, unchanged) ──REST──▶ https://runai.lab  (POST /api/v1/org-unit/projects)
                                                                    │ Run:ai writes kai.resources Project
                                                                    ▼
                                             bnk-runai-tenancy controller ──▶ Gateway, iRule, route, EPP in BNK
agent ──MCP──▶ bnk-tenancy-mcp (ours, read-only) ──get──▶ Gateway status, iRule, bnk-runai-usage ledger
```

The demo script is `platform/mcp/demo.sh`, and it uses nothing but curl and jq as the MCP client.

## NVIDIA's runai-mcp-server, as found

Source: `runai-mcp-server/` in dsx-ai-factory/run-ai. I read it at our pinned ref `59e288d7` and at
`main` (`3e0dfd6d`, 2026-10-15); the transport and auth code is the same in both. I also pulled the
published image and probed it live with curl against a mock OIDC upstream (Docker, 2026-10-15).

SDK and transports. It uses `github.com/modelcontextprotocol/go-sdk` v1.8.0. `serve --transport stdio`
(default) or `--transport http` (env `RUNAI_TRANSPORT=http`). HTTP serves `/mcp` on
`RUNAI_MCP_LISTEN_PORT` (default 8080), `/healthz`, RFC 9728 metadata at
`/.well-known/oauth-protected-resource/mcp`, and Prometheus on `RUNAI_MCP_METRICS_PORT` (9090).

Image. NVIDIA does publish it: `nvcr.io/nvidia/runai/runai-mcp-server`, tags `2.26.13`, `2.26.27`,
`2.26.50`, anonymous pull works, linux/amd64 and arm64. We pin `2.26.50` by index digest in
`platform/versions.env`. That build says `serverVersion=2.26.50` and has 27 tools with write tools on;
the source at `59e288d7` has more tools (templates, AI applications), but `manage_org_unit` has the same
input schema in both. If the published image misbehaves against our control plane,
`platform/mcp/build-runai-mcp.sh` builds the same binary from source at `59e288d7` with NVIDIA's own
Dockerfile and (with `PUSH=1`) pushes `docker.io/iracic82/runai-mcp-server:0.0.0`.

Write tools are off unless `--allow-write-tools` / `RUNAI_ALLOW_WRITE_TOOLS=true`. Each write tool takes
`dryRun` (preview with before/after/diff, nothing changes upstream) and `confirm`. Update and delete need
a confirmation: the server asks the user through MCP elicitation if the client supports it, otherwise it
returns `confirmation_required` until the call carries `confirm=true`. Create does not need it.

### Auth

stdio mode uses `RUNAI_CLIENT_ID`/`RUNAI_CLIENT_SECRET` (client credentials). HTTP mode has no credentials
of its own. At startup it fetches `${RUNAI_BASE_URL}/.well-known/openid-configuration` and the JWKS it
names. Every request to `/mcp` must carry `Authorization: Bearer <JWT>`; the server verifies signature,
issuer and expiry against that JWKS, answers 401 with a `WWW-Authenticate: Bearer resource_metadata=…`
challenge otherwise, and forwards the same token on every Run:ai REST call. So the agent acts with
exactly the Run:ai permissions of whoever's token it holds, and the MCP server can't do more than that
user could in the UI. For the demo the token comes from `POST https://runai.lab/api/v1/token`
(`grantType: password`, admin@acme.lab). NVIDIA's docs have interactive clients get it through OAuth
against the Keycloak client `runai-mcp`.

### Is it stateless? No.

`internal/http/oauth.go` builds the handler with
`mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)`. Nil options means
`Stateless: false`. I confirmed what that does on the wire with image 2.26.50:

- `initialize` answers with an `Mcp-Session-Id` header and a `text/event-stream` body.
- `tools/list` without that header fails: `method "tools/list" is invalid during session initialization`.
  A client has to initialize, keep the id, and send it on every call.
- A sessionless 2026-07-28 request (`_meta` protocol version, SEP-2575) is refused with
  `-32022 protocol version "2026-07-28" is not supported`.

Sessions live in a map inside the process, and `SessionTimeout` is unset, so idle sessions are never
reaped. The confirm-prompt bookkeeping (`elicitInFlight`) is per process too. Two replicas behind a
plain Service break as soon as a call lands on the pod that didn't see `initialize`. The parts that
matter for security are already per request: the bearer token is verified and forwarded on every call,
and no Run:ai credential is cached.

We run it unchanged and do not fork it. `platform/mcp/install.sh` deploys one replica; scaling it needs
session affinity on `Mcp-Session-Id` at the proxy. The demo client carries the session id for this
server only.

Upstream feedback for NVIDIA (small change, worth filing):

1. Pass `&mcp.StreamableHTTPOptions{Stateless: true}` in `protectedMCPHandler`. Every tool is already a
   single request/response over REST with the caller's own token, so nothing depends on the session.
2. The one feature that does need a session is elicitation for update/delete confirmation. Stateless mode
   rejects server-to-client requests, and the tools already fall back to `confirmation_required` plus
   `confirm=true` when the client can't elicit (`common.ConfirmWrite`), so behaviour degrades cleanly.
   With go-sdk v1.8.0 that also enables the 2026-07-28 sessionless protocol.
3. If sessions stay, set `SessionTimeout` so abandoned sessions don't accumulate.
4. `JSONResponse: true` would make simple clients (curl, gateways that buffer) easier; optional.

### Tools (image 2.26.50, write tools on)

`get_cluster_infrastructure_health`, `get_cluster_metrics`, `get_cluster_physical_inventory`,
`get_node_pods`, `get_org_unit_metrics`, `get_workload_effective_policy`, `get_workload_history`,
`get_workload_metrics`, `get_workload_pods`, `get_workload_spec`, `get_workload_status`,
`get_workloads_summary`, `list_credentials`, `list_department_resources`, `list_departments`,
`list_node_pools`, `list_project_resources`, `list_projects`, `list_system_settings`, `list_workloads`,
`manage_credentials`, `manage_node_pool`, `manage_org_unit`, `manage_workload_lifecycle`,
`submit_workload`, `update_system_settings`, `whoami`.

Projects are created with `manage_org_unit` (annotations: destructive, not read-only). Required fields are
`action` and `orgType`; for create also `name`, `clusterId` and `spec` with a quota line per node pool:

```json
{
  "action": "create", "orgType": "project", "name": "team-a",
  "clusterId": "<cluster uuid from list_node_pools>",
  "spec": {
    "parentId": "<department id from list_departments>",
    "quota": [{"nodePoolName": "default", "gpuDeserved": 1}]
  },
  "dryRun": true
}
```

Other `spec` fields: `description`, `requestedNamespace` (leave it out, as with the REST API),
`enforceRunaiScheduler`, `defaultNodePools`, `schedulingRules`; quota lines also take `gpuLimit`,
`gpuOverQuotaWeight`, `cpu*`, `memory*`, `rank`. The tool resolves `nodePoolName` to the node pool id and
calls `POST /api/v1/org-unit/projects`, the same call labctl makes. `list_node_pools` returns
`nodePoolName`, `nodePoolId`, `clusterId`; `list_departments` returns `id`, `name`, `clusterId`.

## bnk-tenancy-mcp, ours

`controller/cmd/mcp` (package `internal/tenantmcp`), same Go module as the controller, go-sdk v1.8.0.
It answers the question the Run:ai server can't: what did BNK build for this project?

| Tool | Returns |
|---|---|
| `get_tenant_front_door(project)` | VIP and port, `endpoint` (`http://VIP:8000/v1`), whether the address is BNK-assigned (`status.addresses`) or only requested (`spec.addresses`), the Gateway's Programmed condition, the iRule's rate limit in req/s (20 × deserved GPUs unless annotated), the tenant header (`X-BNK-Tenant: <project>`), and `ready` = BNK-assigned VIP and Programmed |
| `get_tenant_usage(project)` | `project.<name>` and `department.<dept>` token totals from ConfigMap `bnk-runai-tenancy/bnk-runai-usage` |
| `list_tenants()` | every Gateway in `f5-bnk` labelled `app.kubernetes.io/managed-by=bnk-runai-tenancy` |

All three are annotated read-only and idempotent. A project name must be a DNS label (`[a-z0-9-]`, at most
63); anything else is a tool error before any API call. A Gateway the controller doesn't manage is
treated as "no tenant", so the tool can't be used to read other Gateways in `f5-bnk`.

### Stateless, and what that means here

The handler is `mcp.NewStreamableHTTPHandler(..., &mcp.StreamableHTTPOptions{Stateless: true,
JSONResponse: true, MaxRequestBodyBytes: 64 KiB})`. No `Mcp-Session-Id` is issued or read, GET and DELETE
on `/mcp` answer 405, and each POST gets one `application/json` response. A client may call
`tools/call` without `initialize`. The process keeps nothing between requests: one `*mcp.Server` with
three static tools, and a controller-runtime client that goes straight to the API server (no informer
cache). That is why the Deployment runs two replicas behind a plain ClusterIP Service with no affinity.

Auth is a static bearer token from a mounted Secret (`--token-file`), required on every request and
compared in constant time over SHA-256 digests so neither content nor length leaks through timing. Missing
or wrong tokens get 401 with `WWW-Authenticate: Bearer`. `/healthz` is open. The token is a service
credential for a read-only view; it doesn't grant anything in Run:ai or BNK. RBAC is read-only and
namespaced: get/list Gateways and get `f5-big-cne-irules` in `f5-bnk`, get the one ConfigMap
`bnk-runai-usage` in `bnk-runai-tenancy`. No Secrets, no Run:ai objects, no writes.

Tests (`internal/tenantmcp/tenantmcp_test.go`) use a fake client and `httptest`: raw JSON-RPC
`initialize`, `tools/list` and `tools/call` without a session id (and assert none comes back), 401 cases,
405 on GET/DELETE, tool errors as `isError` results, and a go-sdk client round trip.

## Files

| Path | What |
|---|---|
| `controller/cmd/mcp/main.go` | bnk-tenancy-mcp entry point, flags `--listen :8090 --token-file --bnk-namespace --ledger-namespace` |
| `controller/internal/tenantmcp/` | tools, stateless HTTP handler, bearer check, tests |
| `controller/Dockerfile.mcp` | distroless nonroot image |
| `controller/Makefile` | `make mcp-image`, `make mcp-push` (`IMG_MCP ?= docker.io/iracic82/bnk-tenancy-mcp:0.1.0`) |
| `controller/config/mcp.yaml` | SA, Roles, Deployment (2 replicas, runai node, read-only rootfs), Service :8090, token Secret instructions |
| `platform/mcp/install.sh` | deploys runai-mcp-server (namespace `runai-mcp`) and bnk-tenancy-mcp |
| `platform/mcp/build-runai-mcp.sh` | fallback: build NVIDIA's server from source at `59e288d7` |
| `platform/mcp/demo.sh` | the demo, curl as the MCP client |

## Running it

```bash
# once, by someone with Docker Hub rights
make -C controller mcp-push
# on the control node
bash platform/mcp/install.sh
bash platform/mcp/demo.sh team-agent 2
```

`install.sh` gives runai-mcp-server `RUNAI_BASE_URL=https://runai.lab` and makes it trust the lab CA with
`SSL_CERT_FILE=/etc/runai-ca/ca.crt` (a ConfigMap of `/etc/runai/ca.crt`). That works because it is a
static Go binary on a scratch image, and Go's `crypto/x509` reads `SSL_CERT_FILE` on Linux. Pods resolve
`runai.lab` through the CoreDNS entry `platform/runai/install.sh` adds.

`demo.sh` does, in order: get a Run:ai token; `initialize` (keeps `Mcp-Session-Id`), `tools/list`,
`list_node_pools`, `list_departments`; `manage_org_unit` create with `dryRun: true` and prints the
preview; the same call with `dryRun: false, confirm: true`; then, sessionless, `tools/list` and
`get_tenant_front_door` on bnk-tenancy-mcp every 5 s until `ready`, and `get_tenant_usage`. The last line
is `VIP=<address>`. The dry run is worth pausing on in the demo: the agent sees exactly what Run:ai would
create before anything changes.

## Claude Code against the lab

Both Services are ClusterIP. On the control node they are reachable directly; from a laptop, port-forward:

```bash
kubectl -n runai-mcp port-forward svc/runai-mcp-server 8080:8080 &
kubectl -n bnk-runai-tenancy port-forward svc/bnk-tenancy-mcp 8090:8090 &

RUNAI_TOKEN=$(curl -s --cacert /etc/runai/ca.crt -X POST https://runai.lab/api/v1/token \
  -H 'Content-Type: application/json' \
  -d '{"grantType":"password","username":"admin@acme.lab","password":"Lab-Admin-1!"}' | jq -r .accessToken)
BNK_TOKEN=$(kubectl -n bnk-runai-tenancy get secret bnk-tenancy-mcp-token -o jsonpath='{.data.token}' | base64 -d)

claude mcp add --transport http runai http://localhost:8080/mcp --header "Authorization: Bearer ${RUNAI_TOKEN}"
claude mcp add --transport http bnk-tenancy http://localhost:8090/mcp --header "Authorization: Bearer ${BNK_TOKEN}"
```

The Run:ai access token is short-lived (Keycloak lifetime), so re-add `runai` with a fresh one when it
expires. NVIDIA's documented interactive setup is OAuth:
`claude mcp add --transport http --client-id runai-mcp runai https://<fqdn>/mcp`. That needs a Keycloak
client `runai-mcp` in realm `runai` with a redirect URI Claude Code can use, and the server exposed on a
name the browser reaches. I did not find that client in the open-source control-plane chart at `59e288d7`;
check `https://runai.lab/auth/admin` → realm runai → Clients before relying on it. The static header is
the reliable path in the lab.

## Open risks

1. OIDC discovery at the base URL. HTTP mode refuses to start unless
   `https://runai.lab/.well-known/openid-configuration` answers 200 with an `issuer` that matches the `iss`
   of tokens from `/api/v1/token`. Keycloak publishes discovery under `/auth/realms/runai/…`; whether the
   control plane also serves it at the root is not verified on our build. `install.sh` checks it and
   warns. If it fails, NVIDIA's expected setup isn't met by the OSS chart and that is more upstream
   feedback (we still won't fork; a reverse-proxy route on ingress-nginx for that one path is the
   smallest workaround).
2. Version skew. The published image is 2.26.50; our control plane is source at `59e288d7`. The server
   probes `GET /_version` with the caller's token before registering tools and gates tools by
   `MinAPIVersion`; a dev build (`0.0.0…`) enables all of them. If any REST shape it calls changed, use
   `build-runai-mcp.sh`.
3. runai-mcp-server is stateful (above): one replica, or sticky sessions.
4. bnk-tenancy-mcp reads its token at start; rotate the Secret, then `kubectl rollout restart`.
5. `make mcp-image` could not be tested on my laptop: `go mod download` inside Docker fails behind the
   corporate TLS proxy (it would fail the same way for `Dockerfile.meter`). The linux/amd64 binary builds
   with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/mcp`; build the image on a machine without
   the proxy. Nothing has been pushed.
6. Not yet run on the lab cluster. Verified so far: unit tests, the curl client functions in `demo.sh`
   against image 2.26.50 (session handshake, SSE parsing, tool errors, dryRun validation) and against
   bnk-tenancy-mcp (sessionless, JSON).
