#!/usr/bin/env bash
# "An agent asks for a tenant, Run:ai creates it, BNK serves it", with curl as the MCP client.
# Run on the control node after platform/mcp/install.sh. Usage: demo.sh <project> [gpus] [department]
#
# Every step is a JSON-RPC message POSTed over MCP Streamable HTTP, exactly as an agent sends it:
#   1. Run:ai MCP (NVIDIA runai-mcp-server): initialize, tools/list, discover the cluster / node pool /
#      department, then manage_org_unit create, first as a dryRun preview and then for real.
#   2. Run:ai turns that into a kai.resources Project; the tenancy controller builds the BNK tenant.
#   3. BNK MCP (bnk-tenancy-mcp): get_tenant_front_door polled until BNK reports the VIP, then usage.
#
# NVIDIA's server keeps MCP sessions, so the script carries its Mcp-Session-Id. bnk-tenancy-mcp is
# stateless: no initialize is needed and no session id is ever sent; every POST stands alone.
set -euo pipefail
PROJECT="${1:-}"; GPUS="${2:-1}"; DEPARTMENT="${3:-${LABCTL_DEPARTMENT:-default}}"
[ -n "$PROJECT" ] || { echo "usage: $0 <project> [gpus] [department]" >&2; exit 2; }
[[ "$PROJECT" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || { echo "project must be a DNS label ([a-z0-9-], <=63)" >&2; exit 2; }
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
DOMAIN="${RUNAI_DOMAIN:-runai.lab}"
CA_FILE="${RUNAI_CA_FILE:-/etc/runai/ca.crt}"
RUNAI_USER="${RUNAI_ADMIN_EMAIL:-admin@acme.lab}"
RUNAI_PASS="${RUNAI_ADMIN_PASSWORD:-Lab-Admin-1!}"
NODEPOOL="${RUNAI_NODEPOOL:-default}"
WAIT="${WAIT_SECONDS:-300}"
PROTO=2025-06-18
for b in curl jq kubectl; do command -v "$b" >/dev/null || { echo "need $b" >&2; exit 1; }; done

RUNAI_MCP="${RUNAI_MCP_URL:-http://$(kubectl -n runai-mcp get svc runai-mcp-server -o jsonpath='{.spec.clusterIP}'):8080/mcp}"
BNK_MCP="${BNK_MCP_URL:-http://$(kubectl -n bnk-runai-tenancy get svc bnk-tenancy-mcp -o jsonpath='{.spec.clusterIP}'):8090/mcp}"
BNK_TOKEN="${BNK_MCP_TOKEN:-$(kubectl -n bnk-runai-tenancy get secret bnk-tenancy-mcp-token -o jsonpath='{.data.token}' | base64 -d)}"

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
show() { printf '  > %s\n' "$*"; }
HDRS="$(mktemp)"; trap 'rm -f "$HDRS"' EXIT
ID=0

# mcp URL TOKEN SESSION METHOD PARAMS -> prints the JSON-RPC result; exits on a JSON-RPC error.
# Accepts either response form the spec allows: application/json or a text/event-stream of messages.
mcp() {
  local url="$1" token="$2" session="$3" method="$4" params="${5:-}" body out
  [ -n "$params" ] || params='{}'
  ID=$((ID + 1))
  body="$(jq -nc --argjson id "$ID" --arg m "$method" --argjson p "$params" '{jsonrpc:"2.0",id:$id,method:$m,params:$p}')"
  local args=(-sS -m 120 -D "$HDRS" -X POST "$url" -H "Authorization: Bearer ${token}"
              -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream'
              -H "MCP-Protocol-Version: ${PROTO}")
  [ -n "$session" ] && args+=(-H "Mcp-Session-Id: ${session}")
  out="$(curl "${args[@]}" -d "$body")" || { echo "  POST $url failed" >&2; exit 1; }
  if grep -qi '^content-type: *text/event-stream' "$HDRS"; then
    out="$(sed -n 's/^data: *//p' <<<"$out" | jq -c --argjson id "$ID" 'select(.id == $id)' | tail -1)"
  fi
  jq -e . >/dev/null 2>&1 <<<"$out" || { echo "  $method: unexpected answer: $(head -c 300 <<<"$out") ($(head -1 "$HDRS"))" >&2; exit 1; }
  if jq -e '.error' >/dev/null <<<"$out"; then echo "  $method: $(jq -c .error <<<"$out")" >&2; exit 1; fi
  jq -c .result <<<"$out"
}
notify() { # JSON-RPC notification: no id, answered 202
  local url="$1" token="$2" session="$3" method="$4"
  curl -sS -m 30 -o /dev/null -X POST "$url" -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' -H "MCP-Protocol-Version: ${PROTO}" -H "Mcp-Session-Id: ${session}" \
    -d "{\"jsonrpc\":\"2.0\",\"method\":\"${method}\"}"
}
tool() { # tool URL TOKEN SESSION NAME ARGS -> structuredContent; exits on isError
  local r; r="$(mcp "$1" "$2" "$3" tools/call "$(jq -nc --arg n "$4" --argjson a "$5" '{name:$n,arguments:$a}')")"
  if [ "$(jq -r '.isError // false' <<<"$r")" = true ]; then
    echo "  $4: $(jq -r '[.content[]?.text] | join(" ")' <<<"$r")" >&2; return 1
  fi
  jq -c '.structuredContent // (.content[0].text | fromjson? // .)' <<<"$r"
}

say "0. The agent signs in to Run:ai (POST https://${DOMAIN}/api/v1/token) and gets a bearer token"
RUNAI_TOKEN="$(curl -sS --cacert "$CA_FILE" -m 20 -X POST "https://${DOMAIN}/api/v1/token" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg u "$RUNAI_USER" --arg p "$RUNAI_PASS" '{grantType:"password",username:$u,password:$p}')" \
  | jq -r '.accessToken // .access_token // empty')"
[ -n "$RUNAI_TOKEN" ] || { echo "Run:ai login as $RUNAI_USER failed" >&2; exit 1; }
show "token for $RUNAI_USER ($(cut -d. -f2 <<<"$RUNAI_TOKEN" | tr '_-' '/+' | base64 -d 2>/dev/null | jq -r '.iss // "?"' 2>/dev/null || echo '?'))"

say "1. Run:ai MCP server ($RUNAI_MCP)"
init="$(mcp "$RUNAI_MCP" "$RUNAI_TOKEN" "" initialize \
  "{\"protocolVersion\":\"${PROTO}\",\"capabilities\":{},\"clientInfo\":{\"name\":\"bnk-demo-agent\",\"version\":\"1.0\"}}")"
SID="$(awk 'tolower($1)=="mcp-session-id:"{print $2}' "$HDRS" | tr -d '\r')"
show "server $(jq -r '.serverInfo.name + " " + .serverInfo.version' <<<"$init"), protocol $(jq -r .protocolVersion <<<"$init")"
if [ -n "$SID" ]; then
  show "it issued Mcp-Session-Id ${SID:0:8}… (stateful upstream; carried on the next calls)"
  notify "$RUNAI_MCP" "$RUNAI_TOKEN" "$SID" notifications/initialized
fi
tools="$(mcp "$RUNAI_MCP" "$RUNAI_TOKEN" "$SID" tools/list)"
show "$(jq -r '.tools | length' <<<"$tools") tools, e.g. $(jq -r '[.tools[].name | select(test("org_unit|project|node_pool|department"))] | join(", ")' <<<"$tools")"
jq -e '.tools[] | select(.name=="manage_org_unit")' >/dev/null <<<"$tools" \
  || { echo "manage_org_unit missing: is RUNAI_ALLOW_WRITE_TOOLS=true on runai-mcp-server?" >&2; exit 1; }

pools="$(tool "$RUNAI_MCP" "$RUNAI_TOKEN" "$SID" list_node_pools '{}')"
CLUSTER_ID="$(jq -r --arg n "$NODEPOOL" '[.nodePools[]? | select(.nodePoolName==$n)][0].clusterId // empty' <<<"$pools")"
[ -n "$CLUSTER_ID" ] || { echo "node pool '$NODEPOOL' not found via list_node_pools" >&2; exit 1; }
show "list_node_pools: '$NODEPOOL' in cluster $CLUSTER_ID"
depts="$(tool "$RUNAI_MCP" "$RUNAI_TOKEN" "$SID" list_departments "$(jq -nc --arg c "$CLUSTER_ID" '{clusterId:$c}')")"
DEPT_ID="$(jq -r --arg n "$DEPARTMENT" '[.departments[]? | select(.name==$n)][0].id // empty' <<<"$depts")"
[ -n "$DEPT_ID" ] || { echo "department '$DEPARTMENT' not found via list_departments" >&2; exit 1; }
show "list_departments: '$DEPARTMENT' is $DEPT_ID"

ARGS="$(jq -nc --arg n "$PROJECT" --arg c "$CLUSTER_ID" --arg d "$DEPT_ID" --arg p "$NODEPOOL" --argjson g "$GPUS" \
  '{action:"create",orgType:"project",name:$n,clusterId:$c,
    spec:{parentId:$d,description:"created by an AI agent over MCP",quota:[{nodePoolName:$p,gpuDeserved:$g}]}}')"
say "2. manage_org_unit create '$PROJECT' (${GPUS} GPU) — dryRun first: preview, nothing changes"
preview="$(tool "$RUNAI_MCP" "$RUNAI_TOKEN" "$SID" manage_org_unit "$(jq -c '. + {dryRun:true}' <<<"$ARGS")")"
show "dryRun=$(jq -r .dryRun <<<"$preview") applied=$(jq -r .applied <<<"$preview"): $(jq -c '.after | {name, clusterId, parentId} + (if .resources then {resources} else {} end)' <<<"$preview" 2>/dev/null | head -c 300)"
say "   … the agent approves; apply with dryRun=false, confirm=true"
created="$(tool "$RUNAI_MCP" "$RUNAI_TOKEN" "$SID" manage_org_unit "$(jq -c '. + {dryRun:false,confirm:true}' <<<"$ARGS")")"
show "applied=$(jq -r .applied <<<"$created") project id $(jq -r .orgUnitId <<<"$created")"

say "3. BNK MCP server ($BNK_MCP) — stateless: no initialize, no session id"
info="$(mcp "$BNK_MCP" "$BNK_TOKEN" "" tools/list)"
show "tools: $(jq -r '[.tools[].name] | join(", ")' <<<"$info")"
say "   get_tenant_front_door('$PROJECT') until BNK serves it (up to ${WAIT}s)"
deadline=$((SECONDS + WAIT)); fd=""
while [ $SECONDS -lt $deadline ]; do
  if fd="$(tool "$BNK_MCP" "$BNK_TOKEN" "" get_tenant_front_door "$(jq -nc --arg p "$PROJECT" '{project:$p}')" 2>/dev/null)" \
     && [ "$(jq -r .ready <<<"$fd")" = true ]; then break; fi
  printf '  . %s\n' "$( [ -n "$fd" ] && jq -r '"vip=\(.vip // "-") programmed=\(.programmedState)"' <<<"$fd" 2>/dev/null || echo 'no tenant yet (Run:ai still setting the project up)')"
  fd=""; sleep 5
done
[ -n "$fd" ] || { echo "BNK tenant for '$PROJECT' not ready after ${WAIT}s (labctl tenant show $PROJECT)" >&2; exit 1; }

say "BNK serves '$PROJECT':"
jq -r '"  VIP          \(.vip):\(.port)   (\(.gateway), Programmed \(.programmedState))",
       "  endpoint     \(.endpoint)",
       "  rate limit   \(.rateLimitRps) req/s (20 per deserved GPU)",
       "  every answer \(.tenantHeader)"' <<<"$fd"
usage="$(tool "$BNK_MCP" "$BNK_TOKEN" "" get_tenant_usage "$(jq -nc --arg p "$PROJECT" '{project:$p}')" || echo '{}')"
jq -r '"  tokens       \(.projectTokens // 0) metered (department \(.department // "-"): \(.departmentTokens // 0))"' <<<"$usage"
echo
echo "VIP=$(jq -r .vip <<<"$fd")"
