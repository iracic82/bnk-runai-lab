#!/usr/bin/env bash
# NVIDIA's Dynamo platform (operator + NATS), as in NVIDIA's Kubernetes quickstart. Discovery stays on the
# chart default, kubernetes, which BNK's Endpoint Picker relies on to name Dynamo workers.
set -euo pipefail
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
DYNAMO_VERSION="${DYNAMO_VERSION:-1.5.1}"
helm upgrade --install dynamo-platform \
  "https://helm.ngc.nvidia.com/nvidia/ai-dynamo/charts/dynamo-platform-${DYNAMO_VERSION}.tgz" \
  --namespace dynamo-system --create-namespace \
  --set global.nats.install=true \
  --wait --timeout 10m
kubectl -n dynamo-system rollout status statefulset/dynamo-platform-nats --timeout=300s
# NATS has no auth: only Dynamo tenant namespaces and dynamo-system may connect (see the file).
kubectl apply -f "$(dirname "${BASH_SOURCE[0]}")/nats-policy.yaml"
kubectl -n dynamo-system get pods
