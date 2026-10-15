#!/usr/bin/env bash
# Cluster + BNK 2.4 for the BNK x Run:ai lab, run on the control node. Adapted from the proven
# bnk-ai-kv-lab setup (same kubeadm, Calico, gretap "pcie" overlay, Multus NADs, GAIE CRDs before BNK,
# bnk-deploy installer, licence), with this lab's node names:
#   control  Kubernetes control plane, learner terminal (was: bnk)
#   dpu      BNK TMM data plane on the simulated BlueField (was: dpu)
#   gpu      fake GPUs, Run:ai cluster agent + scheduler, model pods (was: server)
#   runai    Run:ai control plane (new)
# Run:ai itself is installed afterwards by platform/runai/install.sh.
set -uo pipefail
export DEBIAN_FRONTEND=noninteractive

# git clones the lab before anything else; make/openssl/python3-yaml are the bnk-deploy installer's
# preflight deps (dropping them fails preflight with "make not on PATH"); the rest are base node
# deps. Keep this list in parity with the agentic track. Demo-specific packages (screen,
# python3-flask for the control API) are installed further down.
echo 'DPkg::Lock::Timeout "900";' > /etc/apt/apt.conf.d/99lab-lock-timeout
systemctl disable --now unattended-upgrades apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1 || true
apt-get update -qq
apt-get install -y -qq git apt-transport-https ca-certificates curl gpg containerd make openssl python3-yaml ethtool jq

LAB_ROOT="${LAB_ROOT:-/root/lab}"
REPO_ROOT="${REPO_ROOT:-/opt/bnk-deploy}"
WORKERS="${WORKERS:-dpu gpu runai}"
# The shared library lives inside the lab repo, so TRACK_DIR points at the clone destination, not at
# $0 (Instruqt runs lifecycle scripts from /tmp, so a $0-derived path resolves to / and breaks).
PLATFORM="${LAB_ROOT}/platform"

# Minimal inline clone, because the shared library is inside the thing being cloned. Everything after
# this point comes from git, so this host and the workers cannot drift apart.
_clone() {
  [ -d "$3/.git" ] && return 0
  # Token goes in a one-command auth header (GIT_CONFIG_* env), never in the URL, argv or .git/config.
  local auth=""
  [ -n "${GH_READ_TOKEN:-}" ] && auth="Authorization: Basic $(printf 'x-access-token:%s' "$GH_READ_TOKEN" | base64 | tr -d '\n')"
  GIT_CONFIG_COUNT=$([ -n "$auth" ] && echo 1 || echo 0) \
  GIT_CONFIG_KEY_0="http.https://github.com/.extraheader" GIT_CONFIG_VALUE_0="$auth" \
    git clone -q --depth 1 --branch "$2" "https://$1" "$3" \
    || { echo "SETUP FAILED: could not clone $1. If it is private, set the GH_READ_TOKEN secret." >&2; exit 1; }
}
_clone "${LAB_REPO:-github.com/iracic82/bnk-runai-lab.git}" "${LAB_REF:-main}"        "$LAB_ROOT"
_clone "${BNK_DEPLOY_REPO:-github.com/iracic82/bnk-deploy.git}"  "${BNK_DEPLOY_REF:-main}" "$REPO_ROOT"

# fail, step, node_prereqs, install_kubernetes come from the shared library, now that it is cloned.
# shellcheck disable=SC1091
. "${PLATFORM}/cluster/node-prep.sh"
# shellcheck disable=SC1091
. "${REPO_ROOT}/versions.env"

# Secrets arrive as team level Instruqt secrets, write only, so none is in this repository.
# Checked after the library is sourced so an unset secret fails with a clear message, not set -u noise.
for s in FAR_PULL_B64 BNK_LICENSE_JWT; do
  [ -n "${!s:-}" ] || fail "$s is not set. Add it as a team secret and list it in config.yml."
done
install -d -m 700 /root/.bnk
printf '%s' "$FAR_PULL_B64" > /root/.bnk/cne_pull_64.json
chmod 600 /root/.bnk/cne_pull_64.json
export FAR_PULL_JSON=/root/.bnk/cne_pull_64.json

M="${PLATFORM}/bnk"
INSTALLER="${REPO_ROOT}/install.sh"
[ -x "$INSTALLER" ] || fail "installer not found at $INSTALLER"

# The BNK operator registers its CRDs (F5EPP, F5BigCneIrule, NetPolicy, ...) asynchronously after
# the CNEInstance is applied, and the GAIE InferencePool CRDs are installed separately. A custom
# resource applied before its CRD exists fails, and this script runs without set -e, so such a failure
# would be silent and leave the CR missing (this is exactly what dropped a BNK CR on the first real
# run). retry_apply retries until the CRD has been registered, so ordering between the operator and
# this script no longer has to be exact.
retry_apply() {
  local f="$1" i out tmp=""
  # "retry_apply -" buffers stdin to a temp file first, so the retry loop can re-read it (a pipe
  # can only be read once). Used for the sed-rendered iRule manifests piped in below.
  if [ "$f" = "-" ]; then tmp="$(mktemp)"; cat > "$tmp"; f="$tmp"; fi
  for i in $(seq 1 24); do
    out="$(kubectl apply -f "$f" 2>&1)" && { echo "$out"; [ -n "$tmp" ] && rm -f "$tmp"; return 0; }
    sleep 5
  done
  echo "$out" >&2
  [ -n "$tmp" ] && rm -f "$tmp"
  fail "could not apply $f after retries (its BNK CRD never registered?)"
}

# Like retry_apply, but for NON-critical resources (the per-demo iRules): on exhaustion it WARNS and
# continues instead of failing the whole lab. A single transient f5-validation webhook timeout once
# took the entire setup down with it; a missing iRule only disables that one demo's inline enforcement,
# which the demo's check flags, so it must not be fatal to the other demos or Demo 1.
retry_apply_soft() {
  local f="$1" i out tmp=""
  if [ "$f" = "-" ]; then tmp="$(mktemp)"; cat > "$tmp"; f="$tmp"; fi
  for i in $(seq 1 18); do
    out="$(kubectl apply -f "$f" 2>&1)" && { echo "$out"; [ -n "$tmp" ] && rm -f "$tmp"; return 0; }
    sleep 5
  done
  echo "$out" >&2
  echo "  WARNING: could not apply $f after retries (f5-validation webhook or CRD?); continuing - the demo check will flag it." >&2
  [ -n "$tmp" ] && rm -f "$tmp"
  return 0
}

# Best-effort wait for the F5 validation webhook to be serving before we apply F5 CRs that it gates
# (the iRules). Non-fatal: if it never shows, retry_apply_soft still tolerates the resulting timeout.
wait_f5_webhook() {
  local i
  for i in $(seq 1 36); do
    [ -n "$(kubectl -n f5-bnk get endpoints f5-validation-svc -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null)" ] \
      && { echo "  f5-validation webhook is serving"; return 0; }
    sleep 5
  done
  echo "  WARNING: f5-validation webhook never reported ready; iRule applies may be flaky" >&2
  return 0
}

# TMM can come up with both Multus legs attached (network-status shows net1/net2) yet never register
# them as interfaces 1.1/1.2 (tmctl interface_stat lists only xchg/tap/0.1). The VLAN CRs still report
# "sent", but the external self-IP 10.1.10.201 never answers ARP and every BNK VIP and the grey egress
# are dead. Seen on a fresh lab 2026-10-08; deleting the TMM pod fixed it within 5 s. So: wait for the
# self-IP from the bnk host's external leg, and recreate TMM once if it stays silent.
ensure_tmm_external() {
  local i attempt
  for attempt in 1 2; do
    for i in $(seq 1 18); do
      ping -c1 -W1 10.1.10.201 >/dev/null 2>&1 && { echo "  TMM external self-IP 10.1.10.201 answers"; return 0; }
      sleep 5
    done
    [ "$attempt" = 2 ] && break
    echo "  TMM external self-IP silent after 90s; recreating the TMM pod"
    kubectl delete pod -n f5-bnk -l app=f5-tmm --wait=true
    sleep 20
    kubectl wait -n f5-bnk --for=condition=Ready pod -l app=f5-tmm --timeout=240s || true
  done
  echo "  WARNING: TMM external self-IP 10.1.10.201 still not answering; BNK VIPs will not work" >&2
  return 0
}

node_prereqs
install_kubernetes

step "kubeadm init on $(hostname)"
if kubectl --kubeconfig /etc/kubernetes/admin.conf get nodes >/dev/null 2>&1; then
  echo "  already initialised, skipping"
else
  kubeadm init --pod-network-cidr=192.168.0.0/16 || fail "kubeadm init did not succeed"
fi
export KUBECONFIG=/etc/kubernetes/admin.conf
echo 'export KUBECONFIG=/etc/kubernetes/admin.conf' > /etc/profile.d/kubeconfig.sh

step "Calico"
kubectl create -f "https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/tigera-operator.yaml" 2>/dev/null \
  || kubectl apply --server-side --force-conflicts -f "https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/tigera-operator.yaml"
kubectl create -f "${REPO_ROOT}/ci/calico-installation.yaml" 2>/dev/null \
  || kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/ci/calico-installation.yaml"

step "preparing and joining the workers (${WORKERS}) over ssh"
JOIN_CMD="$(kubeadm token create --ttl 1h --print-join-command)" || fail "could not create a join token"
SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o ConnectTimeout=10 -o LogLevel=ERROR)
wait_for_ssh() {
  local w="$1" i
  for i in $(seq 1 120); do
    ssh "${SSH_OPTS[@]}" "root@${w}" true 2>/dev/null && { echo "  ${w} accepts ssh after $((i*5))s"; return 0; }
    sleep 5
  done
  echo "  ${w} never accepted ssh within 600s" >&2; return 1
}
prep_and_join() {
  local w="$1"
  wait_for_ssh "$w" || return 1
  {
    echo 'set -uo pipefail'; echo 'export DEBIAN_FRONTEND=noninteractive'
    echo "K8S_MINOR='${K8S_MINOR}'"
    cat "${PLATFORM}/cluster/node-prep.sh"
    echo "echo 'DPkg::Lock::Timeout \"900\";' > /etc/apt/apt.conf.d/99lab-lock-timeout"
    echo 'systemctl disable --now unattended-upgrades apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1 || true'
    echo 'apt-get update -qq'
    echo 'apt-get install -y -qq apt-transport-https ca-certificates curl gpg containerd ethtool'
    echo 'node_prereqs'; echo 'install_kubernetes'
    echo 'if [ -f /etc/kubernetes/kubelet.conf ]; then echo "  already joined, skipping"; else'
    echo "${JOIN_CMD}"; echo "fi"
  } | ssh "${SSH_OPTS[@]}" "root@${w}" 'bash -s' 2>&1 | sed "s/^/  [${w}] /"
  return "${PIPESTATUS[0]}"
}
declare -A PID_OF=()
for w in $WORKERS; do prep_and_join "$w" & PID_OF[$w]=$!; done
failed=""
for w in $WORKERS; do wait "${PID_OF[$w]}" || failed="$failed $w"; done
[ -z "$failed" ] || fail "these workers failed to prepare or join:${failed}"

step "waiting for all four nodes Ready"
for _ in $(seq 1 60); do
  [ "$(kubectl get nodes --no-headers 2>/dev/null | grep -c ' Ready ')" -ge 4 ] && break
  sleep 5
done
kubectl get nodes -o wide

step "labelling nodes: dpu runs TMM, gpu runs model pods (fake GPUs), runai runs the Run:ai control plane"
# Only dpu carries app=f5-tmm. A second f5-tmm node panics the lifecycle operator, so server is
# labelled app=workload instead, which is where the sims, Prometheus and Grafana land.
kubectl label node dpu    app=f5-tmm  --overwrite
kubectl label node gpu    app=workload run.ai/simulated-gpu-node-pool=default --overwrite
# The runai node adds capacity for the Run:ai control plane (about 50 services). It is not tainted:
# the control plane's subcharts (Postgres, Keycloak, NATS) don't take a global toleration.
kubectl label node runai  app=runai-control-plane --overwrite

# kubelet's default image GC deletes the largest UNUSED images once the disk passes 85%. The BNK install
# pulls enough to cross that, and the first victim is the baked 15GB genai-perf client (nothing runs it
# until the first comparison) - verified live 2026-10-07: "Removing image to free bytes" at install time,
# then a 15GB re-pull on Run. Raise the thresholds on every node before any of the big pulls happen.
step "kubelet image GC: keep the baked images (thresholds 95/90)"
KGC='f=/var/lib/kubelet/config.yaml
sed -i "/^imageGCHighThresholdPercent:/d; /^imageGCLowThresholdPercent:/d" "$f"
printf "imageGCHighThresholdPercent: 95\nimageGCLowThresholdPercent: 90\n" >> "$f"
systemctl restart kubelet && echo "  kubelet GC 95/90 on $(hostname -s)"'
bash -c "$KGC"
for n in dpu gpu runai; do ssh "${SSH_OPTS[@]}" "root@${n}" "$KGC"; done

step "a storage class, because kubeadm ships none"
# kind bundles a default StorageClass; a kubeadm cluster has none, and BNK needs persistent volumes
# for DSSM, so the installer preflight refuses to continue ("no StorageClass"). local-path is the
# smallest thing that works: one Deployment, node-local directories, no cloud dependency. Its
# WaitForFirstConsumer binding creates each volume on whichever node the pod lands on.
LOCAL_PATH_VERSION="${LOCAL_PATH_VERSION:-v0.0.30}"
kubectl apply -f "https://raw.githubusercontent.com/rancher/local-path-provisioner/${LOCAL_PATH_VERSION}/deploy/local-path-storage.yaml" \
  || fail "could not install the local-path provisioner, so BNK has nowhere to put persistent volumes"
# Mark it default, so anything in the lab that asks for storage without naming a class still gets one.
kubectl patch storageclass local-path \
  -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}' >/dev/null
kubectl -n local-path-storage rollout status deploy/local-path-provisioner --timeout=180s \
  || fail "the local-path provisioner never became ready"
kubectl get sc

# ---- TMM data path: build the two gretap overlays and the Multus NADs BEFORE the BNK install, so TMM
# is BORN with both macvlan legs (interfaces 1.1 external, 1.2 internal). The UDF masters external-net
# on a real NIC and internal-net on a gretap; we have single-NIC nodes, so both are gretap bridges:
#   br0 = internal-net 10.1.20.x (TMM -> pods),  br1 = external-net 10.1.10.x (client -> VIP).
step "data-path gretap overlays (internal br0 + external br1) between dpu and gpu"
DPU_IP="$(getent hosts dpu | awk '{print $1}')"
SRV_IP="$(getent hosts gpu | awk '{print $1}')"
for pair in "dpu ${DPU_IP} ${SRV_IP} 15" "gpu ${SRV_IP} ${DPU_IP} 13"; do
  set -- $pair; node="$1"; me="$2"; peer="$3"; oct="$4"
  # two gretaps on the same node pair need distinct keys (1 internal, 2 external). MTU 1400 is safe
  # under GCP's 1460 underlay plus GRE+key overhead.
  ssh "${SSH_OPTS[@]}" "root@${node}" 'bash -s' -- "$me" "$peer" "10.1.20.${oct}/24" pcie  br0 1 1400 < "${PLATFORM}/cluster/pcie-overlay.sh" | sed "s/^/  [${node} int] /"
  ssh "${SSH_OPTS[@]}" "root@${node}" 'bash -s' -- "$me" "$peer" "10.1.10.${oct}/24" pcie1 br1 2 1400 < "${PLATFORM}/cluster/pcie-overlay.sh" | sed "s/^/  [${node} ext] /"
done

# The bnk host joins external-net too, like the UDF client/jumphost that sits on the external VLAN: a
# third gretap (key 3) into the server's br1, bnk side 10.1.10.11. The bnk and server hosts then route
# the VIP subnet to TMM's external self-IP, so the terminal, demo-api (dashboard buttons) and any pod on
# those nodes reach the BNK VIPs with no Multus annotation. A plain VPC route cannot do this: the cloud
# network drops packets addressed to IPs a VM does not own, which is why every leg here is GRE.
BNK_IP="$(ip -4 route get "$SRV_IP" | grep -oP 'src \K\S+')"
ssh "${SSH_OPTS[@]}" "root@gpu" 'bash -s' -- "$SRV_IP" "$BNK_IP" - pcie2 br1 3 1400 < "${PLATFORM}/cluster/pcie-overlay.sh" | sed "s/^/  [gpu ext+control] /"
bash "${PLATFORM}/cluster/pcie-overlay.sh" "$BNK_IP" "$SRV_IP" 10.1.10.11/24 pcie2 br1 3 1400 | sed "s/^/  [control ext] /"
ip route replace 172.16.201.0/24 via 10.1.10.201 dev br1
ssh "${SSH_OPTS[@]}" "root@gpu" "ip route replace 172.16.201.0/24 via 10.1.10.201 dev br1"

step "Multus + TMM data-path NADs (external-net, internal-net) - applied before TMM exists"
# The BNK installer puts Multus in during its 10-prereqs step, but that is AFTER TMM would need these
# NADs, so install Multus here (same pinned v4.2.0; the installer then sees the CRD and skips). Without
# the NADs present when TMM is created, Multus cannot attach its macvlan legs and the TMM pod never starts.
if ! kubectl get crd network-attachment-definitions.k8s.cni.cncf.io >/dev/null 2>&1; then
  kubectl apply -f "https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/${MULTUS_VERSION:-v4.2.0}/deployments/multus-daemonset.yml" \
    || fail "could not install Multus (needed for the TMM data-path NADs)"
fi
for _ in $(seq 1 30); do kubectl get crd network-attachment-definitions.k8s.cni.cncf.io >/dev/null 2>&1 && break; sleep 2; done
kubectl create namespace f5-bnk --dry-run=client -o yaml | kubectl apply -f -
retry_apply "$M/18-bnk-net-nads.yaml"

# GAIE CRDs go in BEFORE the BNK install so the f5-cne-controller is born watching InferencePool. If
# they land after the control plane, the controller's informer never syncs them and the F5EPP reconciler
# logs "could not find InferencePool" forever (verified live 2026-10-07). BNK 2.4 targets GAIE v1.4.0.
step "GAIE CRDs (v1.4.0) - installed before the control plane so the controller watches InferencePool"
kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/${GAIE_VERSION:-v1.4.0}/manifests.yaml" \
  || fail "could not install the GAIE CRDs (InferencePool etc.)"

step "BNK 2.4 via the shared installer from bnk-deploy"
export BNK_TMM_NODES="dpu"
# local-path, not standard: standard is kind's bundled class and does not exist on a kubeadm cluster.
export BNK_STORAGECLASS=local-path
# AI-LB data path: give TMM two macvlan legs (networkAttachments order = interface order: external-net
# -> 1.1, internal-net -> 1.2), map the extra veths onto the data plane, and disable checksum offload
# because the legs ride a gretap overlay. dynamicRouting stays off - the single genai-perf client uses
# a static route to the VIP via TMM's external self-IP, so we need no BGP.
export BNK_NETWORK_ATTACHMENTS="external-net,internal-net"
export BNK_TMM_MAPRES=true
export BNK_DISABLE_CHECKSUM_OFFLOAD=true
# TMM's data NICs ride the gretap overlays (bridge MTU 1400); keep TMM's MTU at/below that so large
# packets are not silently dropped on the tunnel. 1400 is safe under GCP's 1460 underlay + GRE+key.
export BNK_TMM_MTU=1400
# Set eppNamespaces AT INSTALL, not via a post-install patch: the patch triggers a controller rollout
# that, on a CPU-tight cluster, leaves the new pod Pending (verified live 2026-10-07).
# Run:ai creates a namespace per project; the F5 Endpoint Picker only serves namespaces listed at
# install time (a later patch rolls the controller), so the lab's project namespaces go in now.
export BNK_EPP_NAMESPACES="${BNK_EPP_NAMESPACES:-runai-tenant-a,runai-tenant-b,runai-tenant-c,runai-dynamo}"
# profiles/host.env is sourced by the installer AFTER these exports; older bnk-deploy revisions assign
# BNK_NETWORK_ATTACHMENTS= and BNK_TMM_MTU=1500 bare, which silently wipes ours and TMM comes up with
# no data legs (verified live 2026-10-07). Make those lines respect the caller. No-op once bnk-deploy
# carries the ${VAR:-default} form.
sed -i -E \
  -e 's/^BNK_NETWORK_ATTACHMENTS=[^$"].*$|^BNK_NETWORK_ATTACHMENTS=$/BNK_NETWORK_ATTACHMENTS="${BNK_NETWORK_ATTACHMENTS:-}"/' \
  -e 's/^BNK_NETWORK_ATTACHMENTS= +#.*$/BNK_NETWORK_ATTACHMENTS="${BNK_NETWORK_ATTACHMENTS:-}"/' \
  -e 's/^BNK_TMM_MTU=1500$/BNK_TMM_MTU="${BNK_TMM_MTU:-1500}"/' \
  "${REPO_ROOT}/profiles/host.env"
# The Endpoint Picker watches Services in every BNK_EPP_NAMESPACES entry from startup; a missing
# namespace leaves its cache unsynced and the f5-cne-controller restarts forever (seen live 2026-10-15).
# Run:ai adopts these namespaces when the projects are created in challenge 2.
for ns in ${BNK_EPP_NAMESPACES//,/ }; do kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -; done
# bnk-deploy's last phase is verification. It can catch DSSM (BNK's 3-replica state store) mid-settle:
# the wait loop sees DSSMAvailable=True, the check a second later reads it False (seen 2026-10-15, with
# every pod Running). So if the installer fails, wait for the CNEInstance itself before giving up, and
# fail only if a component other than TMM/Available is still not ready.
if ! "$INSTALLER" --env lab --profile host --skip-license; then
  echo "  installer verification failed; waiting up to 10 min for the BNK components to settle"
  settled=0
  for i in $(seq 1 60); do
    notready=$(kubectl get cneinstance f5-bnk-instance -n f5-bnk \
      -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}' 2>/dev/null | tr ' ' '\n' \
      | grep '=False$' | grep -vE '^(Available|F5TmmAvailable)=' | paste -sd, - || true)
    if [ -z "$notready" ] && ! kubectl get pods -A --no-headers 2>/dev/null | grep -E '^(f5-bnk|f5-cne-core) ' | grep -qE 'CrashLoopBackOff|ImagePullBackOff|ErrImagePull'; then
      echo "  BNK components settled after $((i*10))s"; settled=1; break
    fi
    sleep 10
  done
  if [ "$settled" != 1 ]; then
    kubectl get cneinstance f5-bnk-instance -n f5-bnk -o jsonpath='{range .status.conditions[*]}  {.type}={.status} {.message}{"\n"}{end}' || true
    kubectl get pods -n f5-bnk --no-headers | grep -vE 'Running|Completed' || true
    fail "the BNK install did not complete (still not ready: ${notready:-see conditions above})"
  fi
fi
# Belt and braces: confirm the attachments actually landed on the CNEInstance (this is what was silently
# missing before), and patch them in if not - the operator then re-renders TMM with both macvlan legs.
CNE="$(kubectl get cneinstance -n f5-bnk -o name | head -1)"
if [ -z "$(kubectl get -n f5-bnk "$CNE" -o jsonpath='{.spec.networkAttachments}' 2>/dev/null)" ]; then
  echo "  networkAttachments missing on $CNE - patching them in"
  kubectl patch -n f5-bnk "$CNE" --type merge -p '{"spec":{"networkAttachments":["external-net","internal-net"]}}'
fi

step "applying the licence"
lic_err=""
for attempt in $(seq 1 12); do
  lic_err=$(kubectl apply -n f5-cne-core -f - <<LIC 2>&1 >/dev/null
apiVersion: k8s.f5net.com/v1
kind: License
metadata:
  name: f5-cne-cluster-license
spec:
  operationMode: "connected"
  jwt: "${BNK_LICENSE_JWT}"
LIC
  ) && { lic_err=""; break; }
  case "$lic_err" in
    *"status unknown for quota"*) echo "  licence quota not ready, retry ${attempt}/12"; sleep 10 ;;
    *) break ;;
  esac
done
[ -z "$lic_err" ] || fail "could not apply the License: ${lic_err}"
for _ in $(seq 1 60); do
  [ "$(kubectl get license.k8s.f5net.com f5-cne-cluster-license -n f5-cne-core -o jsonpath='{.status.state}' 2>/dev/null)" = "Active" ] && break
  sleep 5
done

step "waiting for the TMM DaemonSet"
for _ in $(seq 1 120); do
  kubectl get ds -n f5-bnk f5-tmm >/dev/null 2>&1 && \
    [ "$(kubectl get ds -n f5-bnk f5-tmm -o jsonpath='{.status.numberReady}' 2>/dev/null || echo 0)" -ge 1 ] && \
    { echo "  f5-tmm ready"; break; }
  sleep 10
done


step "TMM VLANs + self-IPs (external-vlan 1.1, internal-vlan 1.2)"
retry_apply "$M/19-bnk-net-vlans.yaml"
ensure_tmm_external

step "GatewayClass for BNK (BNK does not ship one)"
# controllerName must be f5.com/<CNEInstance namespace>-f5-cne-controller, or every Gateway sits
# "Waiting for controller" (verified live in bnk-ai-kv-lab 2026-10-07 and again here 2026-10-15).
kubectl apply -f - <<'GWC'
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: f5-gateway-class
spec:
  controllerName: f5.com/f5-bnk-f5-cne-controller
GWC

step "BNK 2.4 ready"
kubectl get nodes -o wide
kubectl get pods -n f5-bnk -o wide | grep -E 'tmm|controller' || true
