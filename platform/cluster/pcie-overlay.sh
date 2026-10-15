#!/usr/bin/env bash
# Build a simulated BNK data-path link between the DPU node and the server node as a gretap overlay.
#
# F5's UDF lab wires TMM's data path over two L2 segments:
#   - internal-net (TMM <-> backend pods)   : macvlan master = br0, a gretap bridge (10.1.20.x)
#   - external-net (client  <-> TMM VIP)    : macvlan master = a real NIC (ens6, 10.1.10.x)
# We have single-NIC GCP nodes and no real second NIC, so we fake BOTH segments as gretap bridges
# between dpu and server. TMM gets one macvlan leg on each (interfaces 1.2 internal, 1.1 external via
# CNEInstance.networkAttachments). The genai-perf client gets a macvlan leg on external-net and routes
# the VIP subnet to TMM's external self-IP - mirroring the UDF client that sits on external-net.
#
# Usage, run on each node (two invocations per node: one internal, one external):
#   pcie-overlay.sh <this-node-ip> <other-node-ip> <bridge-ip/cidr> [IFACE] [BRIDGE] [KEY] [MTU]
# Defaults keep the original internal call working unchanged (pcie/br0/key 1).
# Example internal (dpu): pcie-overlay.sh 10.1.1.20 10.1.1.21 10.1.20.15/24
# Example external (dpu): pcie-overlay.sh 10.1.1.20 10.1.1.21 10.1.10.15/24 pcie1 br1 2 1400
#
# NOTE: GRE is IP protocol 47. Two gretaps between the same pair of node IPs MUST use distinct keys
# (KEY arg) or the kernel collides them onto one tunnel. Intra-sandbox traffic is permitted on Instruqt.
set -euo pipefail
LOCAL="${1:?this node ip}"; REMOTE="${2:?other node ip}"; BRIP="${3:?bridge cidr}"
IFACE="${4:-pcie}"; BRIDGE="${5:-br0}"; KEY="${6:-1}"; MTU="${7:-1462}"

modprobe ip_gre 2>/dev/null || true

# Explicit, unique MACs. The lab VMs are clones of one image and share /etc/machine-id, and systemd
# derives "persistent" MACs from machine-id + interface name - so br1 on two nodes gets the SAME MAC and
# each node drops the other's frames as its own looped traffic (verified live 2026-10-07: the bnk<->server
# leg passed no ARP until the MACs were made unique). Derive them from the addresses instead.
hexip() { local IFS=.; set -- $1; printf '%02x:%02x:%02x:%02x' "$1" "$2" "$3" "$4"; }
IF_MAC="02:$(printf '%02x' "$KEY"):$(hexip "$LOCAL")"

# key makes two gretaps on the same endpoint pair distinct (internal key 1, external key 2).
ip link del "$IFACE" 2>/dev/null || true
ip link add "$IFACE" address "$IF_MAC" type gretap remote "$REMOTE" local "$LOCAL" ttl 255 key "$KEY"
ip link set "$IFACE" mtu "$MTU"

if [ "$BRIP" = "-" ]; then
  # join mode: add this tunnel as an extra port of a bridge that already exists (e.g. a third node's
  # leg on br1); the bridge and its address stay as they are.
  ip link set "$IFACE" master "$BRIDGE"
  ip link set "$IFACE" up
else
  # Idempotent: tear down a previous attempt so a restart re-runs cleanly.
  ip link del "$BRIDGE" 2>/dev/null || true
  ip link add "$BRIDGE" address "02:b0:$(hexip "${BRIP%/*}")" type bridge
  ip link set "$IFACE" master "$BRIDGE"
  ip link set "$IFACE" up
  ip link set "$BRIDGE" up
  ip addr add "$BRIP" dev "$BRIDGE"
  ip link set "$BRIDGE" mtu "$MTU"
fi

# Overlay sysctls (verified live 2026-10-07, part of the config that first served the BNK VIP):
#  - rp_filter=0: decapsulated frames arrive on the gretap port but the route back to their source is
#    the bridge, so strict reverse-path filtering drops them (ARP included - ARP checks the source route).
#  - bridge-nf-call-*=0: otherwise bridged gretap traffic is pushed through the host's iptables, where
#    Calico's FORWARD chain drops it. Safe here: Calico runs VXLAN over veths, the only Linux bridges on
#    these nodes are the overlay bridges.
#  - ip_forward=1: TMM's legs and the client route through these segments.
modprobe br_netfilter 2>/dev/null || true
sysctl -qw net.ipv4.ip_forward=1
sysctl -qw net.bridge.bridge-nf-call-iptables=0 net.bridge.bridge-nf-call-arptables=0 net.bridge.bridge-nf-call-ip6tables=0 2>/dev/null || true
for i in all default "$IFACE" "$BRIDGE"; do sysctl -qw "net.ipv4.conf.${i}.rp_filter=0" 2>/dev/null || true; done
# persist across a reboot of the node
cat > /etc/sysctl.d/90-bnk-overlay.conf <<EOF
net.ipv4.ip_forward=1
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
net.bridge.bridge-nf-call-iptables=0
net.bridge.bridge-nf-call-arptables=0
net.bridge.bridge-nf-call-ip6tables=0
EOF

echo "overlay up: gretap $IFACE ($LOCAL -> $REMOTE, key $KEY, $IF_MAC) bridged into $BRIDGE $BRIP mtu $MTU"
