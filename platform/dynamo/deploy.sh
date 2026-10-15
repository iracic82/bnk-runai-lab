#!/usr/bin/env bash
# Deploy NVIDIA Dynamo (frontend + 2 Mocker workers) as a Run:ai project, behind BNK 2.4 with BNK's
# Endpoint Picker in Dynamo mode: BNK picks the Dynamo worker from Dynamo's own KV-cache events.
#
#   deploy.sh          create or update the Run:ai project and the DynamoGraphDeployment
#   deploy.sh delete   remove the DynamoGraphDeployment and the Run:ai project
#
# Steps: Dynamo platform present -> Run:ai project "dynamo" (1 GPU deserved) -> its namespace -> mark the
# namespace as running Dynamo (the tenancy controller then renders an F5EPP with engine dynamo) -> the
# DynamoGraphDeployment -> wait for the workers and for BNK's picker.
set -euo pipefail
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Run:ai projects are created through the Run:ai API (its admission webhook refuses kubectl writes to
# kai.resources Projects); labctl does that and waits for the project to be Ready with its namespace.
LABCTL="${LABCTL:-$(command -v labctl || echo "$HERE/../../labctl/labctl")}"
PROJECT="${PROJECT:-dynamo}"
GRAPH=mocker
MODE="${1:-deploy}"

say() { printf '\n== %s\n' "$*"; }
die() { echo "ERROR: $*" >&2; exit 1; }

wait_for() { # wait_for <seconds> <description> <command...>
  local t=$1 what=$2; shift 2
  for _ in $(seq 1 "$t"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done
  die "timed out after ${t}s waiting for $what"
}

project_ns() { kubectl get projects.kai.resources "$PROJECT" -o jsonpath='{.status.namespace}' 2>/dev/null; }

if [ "$MODE" = delete ]; then
  ns=$(project_ns || true)
  [ -n "$ns" ] && kubectl -n "$ns" delete dynamographdeployment "$GRAPH" --ignore-not-found
  "$LABCTL" tenant delete "$PROJECT" --no-wait
  exit 0
fi
[ "$MODE" = deploy ] || die "usage: $0 [deploy|delete]"

say "Dynamo platform (NVIDIA operator)"
kubectl get crd dynamographdeployments.nvidia.com >/dev/null 2>&1 \
  || die "the Dynamo operator is not installed; run $HERE/install.sh"
kubectl -n dynamo-system get pods --no-headers

say "Run:ai project $PROJECT"
if ! kubectl get projects.kai.resources "$PROJECT" >/dev/null 2>&1; then
  # 1 GPU deserved, rank Low (priority 50); DEPARTMENT if set, else Run:ai's "default".
  "$LABCTL" tenant create "$PROJECT" --gpu 1 --priority 50 ${DEPARTMENT:+--department "$DEPARTMENT"}
fi
wait_for 180 "Run:ai to give project $PROJECT a namespace" bash -c "[ -n \"\$(kubectl get projects.kai.resources $PROJECT -o jsonpath='{.status.namespace}')\" ]"
NS=$(project_ns)
wait_for 120 "namespace $NS" kubectl get namespace "$NS"
echo "namespace: $NS"

say "Tell the tenancy controller this project runs NVIDIA Dynamo"
kubectl annotate namespace "$NS" --overwrite \
  tenancy.f5.com/engine=dynamo tenancy.f5.com/dynamo-graph="$GRAPH"
# The same marker as a label: Dynamo's NATS only accepts namespaces carrying it (nats-policy.yaml).
kubectl label namespace "$NS" --overwrite tenancy.f5.com/engine=dynamo

say "DynamoGraphDeployment $GRAPH"
kubectl -n "$NS" create secret generic hf-token-secret --from-literal=HF_TOKEN="${HF_TOKEN:-}" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS" apply -f "$HERE/dgd.yaml"
# First pull of the Dynamo image (about 0.7 GB) and the tokenizer download take a while.
kubectl -n "$NS" wait "dynamographdeployment/$GRAPH" --for=condition=Ready --timeout=900s

say "BNK tenant (written by the tenancy controller)"
wait_for 180 "Gateway f5-bnk/$PROJECT" kubectl get gateways.gateway.networking.k8s.io -n f5-bnk "$PROJECT"
VIP=$(kubectl get gateways.gateway.networking.k8s.io -n f5-bnk "$PROJECT" -o jsonpath='{.spec.addresses[0].value}')
wait_for 180 "BNK's Endpoint Picker in Dynamo mode" bash -c \
  "[ \"\$(kubectl -n $NS get f5-epps model-pool-epp -o jsonpath='{.spec.engine}')\" = dynamo ]"
kubectl -n "$NS" rollout status deploy/model-pool-epp --timeout=300s
echo "VIP: $VIP:8000"

say "Waiting for a request through BNK to be served by a Dynamo worker"
served() {
  curl -s -m 10 "http://$VIP:8000/v1/chat/completions" -H 'Content-Type: application/json' \
    -d '{"model":"Qwen/Qwen3-0.6B","max_tokens":4,"nvext":{"extra_fields":["worker_id"]},"messages":[{"role":"user","content":"ready?"}]}' \
    | grep -q decode_worker_id
}
wait_for 300 "a 200 from a Dynamo worker through $VIP" served

say "Done"
kubectl -n "$NS" get pods -o wide
echo
echo "Try: curl -s http://$VIP:8000/v1/models"
