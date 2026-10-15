# Shared by every node in the cluster. track_scripts/setup-bnk sources it locally, and pipes it over
# ssh to each worker so the workers run the same code without needing a checkout of their own.
#
# Sourced, not executed. It defines functions and leaves the caller in charge of order.
#
# Nothing in here may reference the repository or any file outside itself, because on a worker it
# arrives as text on stdin with no clone alongside it.
#
fail() { echo; echo "SETUP FAILED: $*" >&2; exit 1; }
step() { echo; echo "### $*"; }

# Clone <repo> <ref> <dest>. The token is stripped from any output, so a clone failure cannot print
# the credential into the track log.
clone() {
  [ -d "$3/.git" ] && return 0
  local url
  # Token goes in a one-command auth header (GIT_CONFIG_* env), never in the URL, argv or .git/config.
  local auth=""
  [ -n "${GH_READ_TOKEN:-}" ] && auth="Authorization: Basic $(printf 'x-access-token:%s' "$GH_READ_TOKEN" | base64 | tr -d '\n')"
  GIT_CONFIG_COUNT=$([ -n "$auth" ] && echo 1 || echo 0) \
  GIT_CONFIG_KEY_0="http.https://github.com/.extraheader" GIT_CONFIG_VALUE_0="$auth" \
    git clone -q --depth 1 --branch "$2" "https://$1" "$3" \
    || fail "could not clone $1. If it is private, set the GH_READ_TOKEN secret."
}

node_prereqs() {
  step "node prerequisites on $(hostname)"
  # TMM requests 4Gi of hugepages-2Mi and the operator keeps that request even if you override
  # advanced.tmm.resources, so this is mandatory on any node TMM may land on. Every node gets it,
  # because which node TMM lands on is a label decision made later.
  echo "vm.nr_hugepages = 2048" > /etc/sysctl.d/90-bnk-hugepages.conf
  sysctl -p /etc/sysctl.d/90-bnk-hugepages.conf
  grep HugePages_Total /proc/meminfo

  swapoff -a && sed -i '/ swap / s/^/#/' /etc/fstab
  modprobe overlay && modprobe br_netfilter
  printf 'overlay\nbr_netfilter\n' > /etc/modules-load.d/k8s.conf
  cat > /etc/sysctl.d/99-k8s.conf <<'SYS'
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
# Run:ai + BNK + Prometheus exhaust Ubuntu's default 128 inotify instances; Dynamo's NATS config
# reloader then dies with "too many open files" (seen live).
fs.inotify.max_user_instances       = 1024
fs.inotify.max_user_watches         = 524288
SYS
  sysctl --system >/dev/null

  # Keep routing-policy rules that systemd-networkd did not create. On these GCP VMs the Google guest
  # agent restarts now and then and restarts systemd-networkd with it (journal, 2026-10-08), and networkd
  # by default deletes every "foreign" ip rule when it starts. That wiped BNK CSRC's per-pod egress
  # rules on the server node, so grey left as the node IP instead of the SNAT address until the grey
  # pods were recreated. Calico's docs give the same advice for its own rules on networkd hosts.
  mkdir -p /etc/systemd/networkd.conf.d
  printf '[Network]\nManageForeignRoutingPolicyRules=no\nManageForeignRoutes=no\n' \
    > /etc/systemd/networkd.conf.d/10-keep-foreign.conf
  # and stop the guest agent from managing network interfaces at all
  if [ -d /etc/default ] && ! grep -q '^\[NetworkInterfaces\]' /etc/default/instance_configs.cfg 2>/dev/null; then
    printf '[NetworkInterfaces]\nsetup = false\n' >> /etc/default/instance_configs.cfg
  fi
  systemctl restart systemd-networkd || true
}

install_kubernetes() {
  step "Kubernetes ${K8S_MINOR} with containerd on $(hostname)"
  mkdir -p /etc/containerd && containerd config default > /etc/containerd/config.toml
  sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
  systemctl restart containerd

  mkdir -p /etc/apt/keyrings
  curl -fsSL "https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/deb/Release.key" \
    | gpg --dearmor --yes -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
  echo "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/deb/ /" \
    > /etc/apt/sources.list.d/kubernetes.list
  apt-get update -qq
  apt-get install -y -qq kubelet kubeadm kubectl && apt-mark hold kubelet kubeadm kubectl >/dev/null
}
