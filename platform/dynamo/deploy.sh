#!/usr/bin/env bash
# Deploy NVIDIA Dynamo (frontend + Mocker workers) as a Run:ai project, served behind BNK 2.4.
#
#   deploy.sh          aggregated: 1 frontend + 2 Mocker decode workers (default)
#   deploy.sh disagg   switch the workers to 1 prefill + 1 decode
#   deploy.sh delete   remove the Dynamo pods and the Run:ai project
#
# Steps: Run:ai project "dynamo" (1 GPU deserved) -> wait for its namespace and the BNK tenant the
# tenancy controller creates -> etcd + NATS -> Dynamo -> HTTPRoute on the tenant Gateway -> wait.
set -euo pipefail
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Run:ai projects are created through the Run:ai API (its admission webhook refuses kubectl writes to
# kai.resources Projects); labctl does that and waits for the project to be Ready with its namespace.
LABCTL="${LABCTL:-$(command -v labctl || echo "$HERE/../../labctl/labctl")}"
PROJECT="${PROJECT:-dynamo}"
MODE="${1:-agg}"

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
  if [ -n "$ns" ]; then
    kubectl -n "$ns" delete -f "$HERE/route.yaml" -f "$HERE/disagg.yaml" -f "$HERE/agg.yaml" -f "$HERE/infra.yaml" --ignore-not-found
  fi
  "$LABCTL" tenant delete "$PROJECT" --no-wait
  exit 0
fi
case "$MODE" in agg|disagg) ;; *) die "usage: $0 [agg|disagg|delete]";; esac

say "Run:ai project $PROJECT"
if ! kubectl get projects.kai.resources "$PROJECT" >/dev/null 2>&1; then
  # 1 GPU deserved, rank Low (priority 50); DEPARTMENT if set, else Run:ai's "default".
  "$LABCTL" tenant create "$PROJECT" --gpu 1 --priority 50 ${DEPARTMENT:+--department "$DEPARTMENT"}
fi
wait_for 180 "Run:ai to give project $PROJECT a namespace" bash -c "[ -n \"\$(kubectl get projects.kai.resources $PROJECT -o jsonpath='{.status.namespace}')\" ]"
NS=$(project_ns)
wait_for 120 "namespace $NS" kubectl get namespace "$NS"
echo "namespace: $NS"

say "BNK tenant (written by the tenancy controller)"
wait_for 180 "Gateway f5-bnk/$PROJECT" kubectl get gateways.gateway.networking.k8s.io -n f5-bnk "$PROJECT"
VIP=$(kubectl get gateways.gateway.networking.k8s.io -n f5-bnk "$PROJECT" -o jsonpath='{.spec.addresses[0].value}')
echo "VIP: $VIP:8000"

say "Dynamo runtime services (etcd, NATS)"
kubectl -n "$NS" apply -f "$HERE/infra.yaml"
kubectl -n "$NS" rollout status deploy/dynamo-etcd --timeout=600s
kubectl -n "$NS" rollout status deploy/dynamo-nats --timeout=600s

say "Dynamo frontend + Mocker workers ($MODE)"
kubectl -n "$NS" apply -f "$HERE/agg.yaml"
if [ "$MODE" = disagg ]; then
  # Same Deployment name as the aggregated decode workers: this replaces them.
  kubectl -n "$NS" apply -f "$HERE/disagg.yaml"
else
  kubectl -n "$NS" delete deploy dynamo-prefill --ignore-not-found
fi
kubectl -n "$NS" apply -f "$HERE/route.yaml"

# First pull of the Dynamo image (about 0.7 GB) and the tokenizer download take a while.
kubectl -n "$NS" rollout status deploy/dynamo-frontend --timeout=900s
kubectl -n "$NS" rollout status deploy/dynamo-decode --timeout=900s
if [ "$MODE" = disagg ]; then kubectl -n "$NS" rollout status deploy/dynamo-prefill --timeout=900s; fi

say "Waiting for the workers to register with the frontend"
model_up() {
  kubectl -n "$NS" exec deploy/dynamo-frontend -c frontend -- \
    python3 -c "import urllib.request,sys; sys.exit(0 if b'Qwen/Qwen3-0.6B' in urllib.request.urlopen('http://127.0.0.1:8000/v1/models',timeout=3).read() else 1)"
}
wait_for 300 "the model to appear on the Dynamo frontend" model_up

say "Done"
kubectl -n "$NS" get pods -o wide
echo
echo "Try: curl -s http://$VIP:8000/v1/models"
