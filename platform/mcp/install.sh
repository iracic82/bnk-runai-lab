#!/usr/bin/env bash
# Agent control of the AI factory over MCP (docs/mcp.md). Run on the control node after
# platform/runai/install.sh (lab CA at /etc/runai/ca.crt, runai.lab in CoreDNS) and the tenancy controller.
#
#   1. NVIDIA's runai-mcp-server, unchanged: Streamable HTTP on :8080, write tools on, RUNAI_BASE_URL=https://runai.lab.
#      Image: NVIDIA's published nvcr.io build (anonymous pull), or the one build-runai-mcp.sh builds from
#      source at RUNAI_REF (set RUNAI_MCP_IMAGE=docker.io/iracic82/runai-mcp-server:0.0.0).
#   2. Our bnk-tenancy-mcp (controller/config/mcp.yaml): stateless, read-only BNK tenant tools on :8090.
#
# Env: RUNAI_MCP_IMAGE, BNK_TENANCY_MCP_IMAGE, RUNAI_DOMAIN, RUNAI_CA_FILE, LAB_ROOT
set -euo pipefail
LAB_ROOT="${LAB_ROOT:-/root/lab}"
# shellcheck disable=SC1091
. "${LAB_ROOT}/platform/versions.env"
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
DOMAIN="${RUNAI_DOMAIN:-runai.lab}"
CA_FILE="${RUNAI_CA_FILE:-/etc/runai/ca.crt}"
NS=runai-mcp
TENANCY_NS=bnk-runai-tenancy
step() { echo; echo "==== $*"; }
die() { echo "ERROR: $*" >&2; exit 1; }

[ -r "$CA_FILE" ] || die "lab CA $CA_FILE missing; run platform/runai/install.sh first"
kubectl get ns "$TENANCY_NS" >/dev/null 2>&1 || die "namespace $TENANCY_NS missing; deploy the tenancy controller first (controller/config/deploy.yaml)"

step "preflight: the Run:ai endpoints runai-mcp-server needs at startup and on first request"
# HTTP transport: OIDC discovery at RUNAI_BASE_URL (JWKS for inbound bearer tokens), then GET /_version
# (control-plane version probe, made with the caller's token) before any tool is registered.
code="$(curl -s -o /dev/null -w '%{http_code}' --cacert "$CA_FILE" -m 15 "https://${DOMAIN}/.well-known/openid-configuration" || true)"
[ "$code" = 200 ] || echo "  WARNING: https://${DOMAIN}/.well-known/openid-configuration answered ${code}; runai-mcp-server will not start (see docs/mcp.md, risks)" >&2

step "NVIDIA runai-mcp-server (${RUNAI_MCP_IMAGE})"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -
# The server is a Go binary on a scratch image: SSL_CERT_FILE makes crypto/x509 trust the lab CA for
# https://runai.lab (it replaces the image's bundle, which is fine: runai.lab is the only upstream).
kubectl -n "$NS" create configmap runai-lab-ca --from-file=ca.crt="$CA_FILE" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f - <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runai-mcp-server
  namespace: ${NS}
  labels: {app.kubernetes.io/name: runai-mcp-server}
spec:
  # One replica: the upstream HTTP transport keeps MCP sessions (Mcp-Session-Id) in process memory, so
  # more replicas need session affinity. docs/mcp.md has the upstream feedback.
  replicas: 1
  selector:
    matchLabels: {app.kubernetes.io/name: runai-mcp-server}
  template:
    metadata:
      labels: {app.kubernetes.io/name: runai-mcp-server}
    spec:
      nodeSelector: {app: runai-control-plane}
      automountServiceAccountToken: false
      securityContext: {runAsNonRoot: true, runAsUser: 65532, runAsGroup: 65532, seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: mcp
          image: ${RUNAI_MCP_IMAGE}
          args: ["serve"]
          env:
            - {name: RUNAI_TRANSPORT, value: http}
            - {name: RUNAI_BASE_URL, value: "https://${DOMAIN}"}
            - {name: RUNAI_ALLOW_WRITE_TOOLS, value: "true"}
            - {name: RUNAI_MCP_LISTEN_PORT, value: "8080"}
            - {name: RUNAI_MCP_METRICS_PORT, value: "9090"}
            - {name: SSL_CERT_FILE, value: /etc/runai-ca/ca.crt}
          ports:
            - {name: mcp, containerPort: 8080}
            - {name: metrics, containerPort: 9090}
          livenessProbe: {httpGet: {path: /healthz, port: mcp}, periodSeconds: 20}
          readinessProbe: {httpGet: {path: /healthz, port: mcp}, periodSeconds: 5}
          resources:
            requests: {cpu: 20m, memory: 64Mi}
            limits: {memory: 256Mi}
          securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: ["ALL"]}, readOnlyRootFilesystem: true}
          volumeMounts:
            - {name: ca, mountPath: /etc/runai-ca, readOnly: true}
            - {name: tmp, mountPath: /tmp}
      volumes:
        - {name: ca, configMap: {name: runai-lab-ca}}
        - {name: tmp, emptyDir: {}}
---
apiVersion: v1
kind: Service
metadata:
  name: runai-mcp-server
  namespace: ${NS}
  labels: {app.kubernetes.io/name: runai-mcp-server}
spec:
  type: ClusterIP
  selector: {app.kubernetes.io/name: runai-mcp-server}
  ports:
    - {name: mcp, port: 8080, targetPort: mcp}
    - {name: metrics, port: 9090, targetPort: metrics}
YAML
kubectl -n "$NS" rollout status deploy/runai-mcp-server --timeout=180s

step "bnk-tenancy-mcp (${BNK_TENANCY_MCP_IMAGE})"
if ! kubectl -n "$TENANCY_NS" get secret bnk-tenancy-mcp-token >/dev/null 2>&1; then
  kubectl -n "$TENANCY_NS" create secret generic bnk-tenancy-mcp-token \
    --from-literal=token="$(head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 40)" >/dev/null
  echo "  created secret $TENANCY_NS/bnk-tenancy-mcp-token"
fi
sed "s#image: docker.io/iracic82/bnk-tenancy-mcp:.*#image: ${BNK_TENANCY_MCP_IMAGE}#" \
  "${LAB_ROOT}/controller/config/mcp.yaml" | kubectl apply -f -
kubectl -n "$TENANCY_NS" rollout status deploy/bnk-tenancy-mcp --timeout=180s

step "done"
echo "  runai-mcp-server  http://$(kubectl -n "$NS" get svc runai-mcp-server -o jsonpath='{.spec.clusterIP}'):8080/mcp   (Run:ai bearer token)"
echo "  bnk-tenancy-mcp   http://$(kubectl -n "$TENANCY_NS" get svc bnk-tenancy-mcp -o jsonpath='{.spec.clusterIP}'):8090/mcp   (token: secret $TENANCY_NS/bnk-tenancy-mcp-token)"
echo "  demo: bash ${LAB_ROOT}/platform/mcp/demo.sh <project> [gpus]"
