#!/usr/bin/env bash
# Prometheus + Grafana for the BNK 2.4 x Run:ai lab: kube-prometheus-stack on the control node, Grafana
# on hostPort 3000 of the control node (Instruqt "Grafana" tab) with anonymous Viewer access, plus the
# lab's scrape targets, recording rules and the "One Tenant, Two Planes" dashboard.
#
#   install.sh               full install (CRDs, chart, monitors, dashboard)
#   install.sh --crds-only   just the Prometheus Operator CRDs. Run:ai's runai-operator watches
#                            ServiceMonitor/PodMonitor/PrometheusRule and crashes if they are absent,
#                            so platform/runai/install.sh needs them before the Run:ai cluster chart.
#
# Env: LAB_ROOT, MONITORING_NS (monitoring), RELEASE (kube-prometheus-stack)
set -euo pipefail
LAB_ROOT="${LAB_ROOT:-$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/../.." && pwd)}"
# shellcheck disable=SC1091
. "${LAB_ROOT}/platform/versions.env"
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
HERE="${LAB_ROOT}/platform/observability"
NS="${MONITORING_NS:-monitoring}"
RELEASE="${RELEASE:-kube-prometheus-stack}"
CHART_VERSION="${KUBE_PROMETHEUS_STACK_VERSION:?set KUBE_PROMETHEUS_STACK_VERSION in platform/versions.env}"
OPERATOR_VERSION="${PROMETHEUS_OPERATOR_VERSION:?set PROMETHEUS_OPERATOR_VERSION in platform/versions.env}"
GRAFANA_PORT="${GRAFANA_PORT:-3000}"
step() { echo; echo "==== $(date -u +%H:%M:%S) $*"; }
fail() { echo "OBSERVABILITY FAILED: $*" >&2; exit 1; }

# The ten monitoring.coreos.com CRDs of the operator release that matches the chart's appVersion, applied
# server-side (the Prometheus CRD is larger than the client-side last-applied annotation allows).
install_crds() {
  step "Prometheus Operator CRDs ${OPERATOR_VERSION}"
  local url="https://github.com/prometheus-operator/prometheus-operator/releases/download/${OPERATOR_VERSION}/stripped-down-crds.yaml"
  local i
  for i in 1 2 3; do
    kubectl apply --server-side --force-conflicts -f "$url" >/dev/null && break
    [ "$i" = 3 ] && fail "could not apply $url"
    sleep 5
  done
  for crd in servicemonitors podmonitors prometheusrules; do
    kubectl wait --for=condition=Established "crd/${crd}.monitoring.coreos.com" --timeout=60s >/dev/null \
      || fail "CRD ${crd}.monitoring.coreos.com not established"
  done
  echo "  ServiceMonitor, PodMonitor, PrometheusRule (and 7 more) established"
}

if [ "${1:-}" = "--crds-only" ]; then
  install_crds
  exit 0
fi
[ -z "${1:-}" ] || fail "usage: $0 [--crds-only]"

command -v helm >/dev/null || fail "helm not found"
install_crds

step "namespace + Grafana admin credentials"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
# A random admin password, generated once and kept in a Secret; anonymous viewers never need it.
if ! kubectl -n "$NS" get secret grafana-admin >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic grafana-admin \
    --from-literal=admin-user=admin \
    --from-literal=admin-password="$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)" >/dev/null
fi

step "dashboard ConfigMap (Grafana sidecar label grafana_dashboard=1)"
kubectl -n "$NS" create configmap one-tenant-two-planes-dashboard \
  --from-file=one-tenant-two-planes.json="${HERE}/dashboards/one-tenant-two-planes.json" \
  --dry-run=client -o yaml \
  | kubectl label --local -f - grafana_dashboard=1 app.kubernetes.io/part-of=bnk-runai-lab -o yaml \
  | kubectl apply -f - >/dev/null

step "kube-prometheus-stack ${CHART_VERSION}"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update >/dev/null
helm repo update prometheus-community >/dev/null
# CRDs are already in place at the matching version; skip the chart's copy so helm never fights them.
helm upgrade --install "$RELEASE" prometheus-community/kube-prometheus-stack \
  --version "$CHART_VERSION" --namespace "$NS" \
  -f "${HERE}/values.yaml" \
  --set crds.enabled=false \
  --set grafana.admin.existingSecret=grafana-admin \
  --set grafana.admin.userKey=admin-user \
  --set grafana.admin.passwordKey=admin-password \
  --wait --timeout 10m || fail "helm install of kube-prometheus-stack ${CHART_VERSION}"

step "Grafana on hostPort ${GRAFANA_PORT} of the control node"
# The grafana chart has no hostPort value; add it to the container's grafana port. Strategy is Recreate
# (values.yaml), so the old pod releases the port before the new one binds it.
GRAFANA_DEPLOY="$(kubectl -n "$NS" get deploy -l app.kubernetes.io/name=grafana -o jsonpath='{.items[0].metadata.name}')"
[ -n "$GRAFANA_DEPLOY" ] || fail "no Grafana deployment in $NS"
idx="$(kubectl -n "$NS" get deploy "$GRAFANA_DEPLOY" -o json \
  | jq '[.spec.template.spec.containers[].name] | index("grafana")')"
port_idx="$(kubectl -n "$NS" get deploy "$GRAFANA_DEPLOY" -o json \
  | jq --argjson i "$idx" '[.spec.template.spec.containers[$i].ports[].name] | index("grafana")')"
cur="$(kubectl -n "$NS" get deploy "$GRAFANA_DEPLOY" -o jsonpath="{.spec.template.spec.containers[$idx].ports[$port_idx].hostPort}")"
if [ "$cur" != "$GRAFANA_PORT" ]; then
  kubectl -n "$NS" patch deploy "$GRAFANA_DEPLOY" --type json -p \
    "[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/$idx/ports/$port_idx/hostPort\",\"value\":$GRAFANA_PORT}]"
fi
kubectl -n "$NS" rollout status "deploy/$GRAFANA_DEPLOY" --timeout=300s || fail "Grafana did not roll out"

step "lab scrape targets and recording rules"
# The tenancy controller and meter namespace, and gpu-operator / runai, may not exist yet on a partial
# install; create the namespaces so the monitors are in place when those components arrive.
for n in bnk-runai-tenancy gpu-operator runai; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
kubectl apply -f "${HERE}/monitors.yaml"

step "checks"
for i in $(seq 1 60); do
  curl -s -m 3 "http://127.0.0.1:${GRAFANA_PORT}/api/health" | grep -q '"database": *"ok"' && break
  [ "$i" = 60 ] && fail "Grafana does not answer on 127.0.0.1:${GRAFANA_PORT}"
  sleep 2
done
pw="$(kubectl -n "$NS" get secret grafana-admin -o jsonpath='{.data.admin-password}' | base64 -d)"
for i in $(seq 1 30); do
  uid="$(curl -s -m 3 -u "admin:${pw}" "http://127.0.0.1:${GRAFANA_PORT}/api/dashboards/uid/one-tenant-two-planes" \
         | jq -r '.dashboard.uid // empty' 2>/dev/null || true)"
  [ "$uid" = one-tenant-two-planes ] && break
  [ "$i" = 30 ] && fail "dashboard one-tenant-two-planes was not loaded by the Grafana sidecar"
  sleep 2
done
anon="$(curl -s -m 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${GRAFANA_PORT}/api/dashboards/uid/one-tenant-two-planes")"
[ "$anon" = 200 ] || fail "anonymous viewer cannot open the dashboard (HTTP $anon)"
echo "  Grafana http://control:${GRAFANA_PORT}/d/one-tenant-two-planes (anonymous viewer, admin password in secret ${NS}/grafana-admin)"
kubectl -n "$NS" get pods -o wide
