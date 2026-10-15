# Source me: sets NS (the Run:ai project's namespace) and VIP (its BNK front door) for project dynamo.
export KUBECONFIG="${KUBECONFIG:-/etc/kubernetes/admin.conf}"
PROJECT="${PROJECT:-dynamo}"
NS=$(kubectl get projects.kai.resources "$PROJECT" -o jsonpath='{.status.namespace}' 2>/dev/null)
VIP=$(kubectl get gateways.gateway.networking.k8s.io -n f5-bnk "$PROJECT" -o jsonpath='{.spec.addresses[0].value}' 2>/dev/null)
MODEL="${MODEL:-Qwen/Qwen3-0.6B}"
export PROJECT NS VIP MODEL
echo "project=$PROJECT namespace=${NS:-?} vip=${VIP:-?}"
