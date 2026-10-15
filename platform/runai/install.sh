#!/usr/bin/env bash
# Install open-source NVIDIA Run:ai on the lab cluster (real kubeadm cluster with BNK 2.4), pulling
# the images the builder published to Docker Hub. Nothing is compiled here.
#
# It reuses NVIDIA's own deploy functions from local-dev.sh (TLS, namespaces/secrets, control-plane
# chart, Keycloak token, cluster registration, cluster chart) instead of re-implementing them, and
# replaces only the parts that assume Kind: the image registry, ingress, DNS and fake GPUs.
# See docs/build-and-deploy-flow.md.
#
# Env: RUNAI_IMAGE_REGISTRY (default docker.io/iracic82), RUNAI_REF, RUNAI_DOMAIN, RUNAI_ADMIN_PASSWORD
set -euo pipefail
LAB_ROOT="${LAB_ROOT:-/root/lab}"
# shellcheck disable=SC1091
. "${LAB_ROOT}/platform/versions.env"
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
RUNAI_SRC="${RUNAI_SRC:-/root/run-ai}"
RUNAI_IMAGE_REGISTRY="${RUNAI_IMAGE_REGISTRY:-docker.io/iracic82}"
export RUNAI_DOMAIN="${RUNAI_DOMAIN:-runai.lab}"
export RUNAI_ADMIN_EMAIL="${RUNAI_ADMIN_EMAIL:-admin@acme.lab}"
export RUNAI_ADMIN_PASSWORD="${RUNAI_ADMIN_PASSWORD:-Lab-Admin-1!}"
INGRESS_NGINX_VERSION="${INGRESS_NGINX_VERSION:-controller-v1.12.3}"
step() { echo; echo "==== $(date -u +%H:%M:%S) $*"; }

step "Run:ai source @ ${RUNAI_REF} (charts and deploy functions only)"
if [ ! -d "${RUNAI_SRC}/.git" ]; then
  git clone -q https://github.com/dsx-ai-factory/run-ai.git "$RUNAI_SRC"
fi
git -C "$RUNAI_SRC" checkout -q "$RUNAI_REF"

# Load local-dev.sh's functions and settings without running its main(). Its arg parser loops over
# "$@", so it is sourced with no arguments; REPO_ROOT resolves to the clone.
# The copy lives inside the clone: local-dev.sh derives REPO_ROOT (chart paths) from its own location.
_ld="${RUNAI_SRC}/.lab-local-dev-functions.sh"; sed '$d' "${RUNAI_SRC}/local-dev.sh" > "$_ld"   # last line is: main "$@"
set --
# shellcheck disable=SC1090
. "$_ld"; rm -f "$_ld"
set +e; set -uo pipefail   # local-dev.sh sets -e; the helpers below handle their own retries
# check_prereqs (skipped here, it is Kind-specific) sets OS and ARCH through detect_os_arch;
# deploy_runai_cluster reads OS, so call NVIDIA's function directly.
detect_os_arch
# Point every image at Docker Hub instead of Kind's localhost:5001 registry.
REGISTRY_PORT=""; LOCAL_IMAGE_REPO=""
registry() { printf '%s' "$RUNAI_IMAGE_REGISTRY"; }
# Redefine one of NVIDIA's deploy functions with every localhost registry reference pointed at
# Docker Hub. The match is unquoted: the cluster chart writes it inside "key=localhost:...".
# Fails loudly if any localhost reference survives, so a changed upstream never deploys a bad image.
use_hub_registry() {
  local body; body="$(declare -f "$1" | sed "s#localhost:\${REGISTRY_PORT}/\${LOCAL_IMAGE_REPO}#${RUNAI_IMAGE_REGISTRY}#g")"
  if grep -q 'localhost:\${REGISTRY_PORT}' <<<"$body"; then echo "registry rewrite missed a reference in $1" >&2; exit 1; fi
  eval "$body"
}

step "ingress-nginx on the runai node (the Run:ai UI and API sit behind it)"
kubectl apply -f "https://raw.githubusercontent.com/kubernetes/ingress-nginx/${INGRESS_NGINX_VERSION}/deploy/static/provider/baremetal/deploy.yaml"
kubectl -n ingress-nginx patch deploy ingress-nginx-controller --type merge -p '{"spec":{"template":{"spec":{
  "nodeSelector":{"app":"runai-control-plane"},
  "hostNetwork":true,"dnsPolicy":"ClusterFirstWithHostNet"}}}}'
kubectl -n ingress-nginx rollout status deploy/ingress-nginx-controller --timeout=300s
RUNAI_NODE_IP="$(kubectl get node runai -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
# The terminal and the controller reach the UI/API by name.
grep -q " ${DOMAIN}$" /etc/hosts || echo "${RUNAI_NODE_IP} ${DOMAIN}" >> /etc/hosts

step "TLS + namespaces + secrets (NVIDIA local-dev functions)"
generate_tls
create_namespace_and_secrets
# Trust the lab CA on this node, so labctl and curl verify https://${DOMAIN} instead of using -k.
# generate_tls writes it to ${CERT_DIR}/ca.crt (${RUNAI_SRC}/.local-dev/certs).
install -d -m 755 /etc/runai
install -m 644 "${CERT_DIR}/ca.crt" /etc/runai/ca.crt
install -m 644 "${CERT_DIR}/ca.crt" /usr/local/share/ca-certificates/runai-lab-ca.crt
update-ca-certificates >/dev/null 2>&1 || echo "  WARNING: update-ca-certificates failed; tools must pass --cacert /etc/runai/ca.crt" >&2
# Pods resolve the domain to the runai node, where ingress-nginx listens on the host network.
cf="$(kubectl -n kube-system get cm coredns -o jsonpath='{.data.Corefile}')"
if [[ "$cf" != *"${DOMAIN}:53"* ]]; then
  kubectl -n kube-system patch cm coredns --type merge -p "{\"data\":{\"Corefile\":$(printf '%s\n%s:53 {\n    hosts {\n        %s %s\n        fallthrough\n    }\n    cache 30\n}\n' "$cf" "$DOMAIN" "$RUNAI_NODE_IP" "$DOMAIN" | python3 -c 'import sys,json;print(json.dumps(sys.stdin.read()))')}}"
  kubectl -n kube-system rollout restart deploy/coredns && kubectl -n kube-system rollout status deploy/coredns --timeout=120s
fi

step "Run:ai control plane (images from ${RUNAI_IMAGE_REGISTRY})"
# deploy_control_plane passes global.image.registry=localhost:${REGISTRY_PORT}/${LOCAL_IMAGE_REPO};
# rewrite that one argument, then pin the whole release to the runai node.
use_hub_registry deploy_control_plane
# The control plane's subcharts (Postgres, Keycloak, NATS) don't take a global nodeSelector, so the
# release is not pinned; the runai node adds capacity for it and the scheduler spreads the pods.
deploy_control_plane

step "register this cluster in the control plane (NVIDIA local-dev functions)"
CP_CLUSTER_NAME=acme-ai-factory
provision_cluster_credentials

step "Run:ai cluster chart (scheduler + resource management)"
# runai-operator watches ServiceMonitor/PodMonitor/PrometheusRule and exits if their CRDs are absent
# (NVIDIA's local-dev cluster ships them; ours gets them here, Prometheus itself comes later).
bash "${LAB_ROOT}/platform/observability/install.sh" --crds-only
use_hub_registry deploy_runai_cluster
# deploy_runai_cluster passes .local-dev/values-rc.yaml (an empty mapping upstream). The operator turns
# on cluster-wide ResourceClaimTemplates by default, so shared-objects-controller watches
# resource.k8s.io/v1, which only exists from Kubernetes 1.34. On this 1.30 cluster its cache never
# syncs and it crash-loops (seen live). The lab has no DRA, so switch that one sync off.
cat > "${RUNAI_SRC}/.local-dev/values-rc.yaml" <<'VALS'
clusterConfig:
  project-controller:
    clusterWideResourceClaimTemplate: false
VALS
# A release left by a failed install carries its old image names; its uninstall hooks would run
# with them and hang. Remove it without hooks so NVIDIA's function starts from a clean slate.
if helm -n runai status runai-cluster >/dev/null 2>&1 && \
   [[ "$(helm -n runai status runai-cluster -o json | python3 -c 'import sys,json;print(json.load(sys.stdin)["info"]["status"])')" != deployed ]]; then
  helm -n runai uninstall runai-cluster --no-hooks || true
  kubectl -n runai delete job --all --wait=false || true
fi
deploy_runai_cluster

step "fake GPUs on the gpu node"
helm upgrade --install fake-gpu-operator oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
  --version "${FAKE_GPU_OPERATOR_VERSION:-0.2.1}" --namespace gpu-operator --create-namespace \
  --set gpuOperator.enabled=false --set nvidiaDraDriver.enabled=false \
  --set topology.nodePools.default.gpuCount=4 --set topology.nodePools.default.gpuType=Tesla-A10 \
  --wait --timeout 5m
kubectl label node gpu run.ai/simulated-gpu-node-pool=default --overwrite

step "Run:ai ready"
echo "  UI:       https://${DOMAIN}  (user ${ADMIN_EMAIL})"
kubectl get nodes -L run.ai/simulated-gpu-node-pool
kubectl get pods -n runai-backend --no-headers | awk '{print $3}' | sort | uniq -c
kubectl get pods -n runai --no-headers | awk '{print $3}' | sort | uniq -c
