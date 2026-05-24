#!/usr/bin/env bash
# k3s-setup.sh — stand up a k3s cluster with ArgoCD for e2e testing.
#
# Designed for devcontainers on NixOS hosts where Docker/iptables are unavailable.
# Uses k3s with nftables kube-proxy, native containerd snapshotter, and a minimal
# bridge CNI (no flannel). Networking is just enough for pods to start and talk
# to the API server — no real pod-to-pod routing.
#
# Usage:
#   .claude/utils/k3s-setup.sh          # start cluster + install ArgoCD
#   .claude/utils/k3s-setup.sh teardown  # destroy everything

set -euo pipefail

KUBECONFIG_PATH="${HOME}/.kube/config"
K3S_LOG="/tmp/k3s.log"

teardown() {
  echo "Tearing down k3s..."
  sudo /usr/local/bin/k3s-killall.sh 2>/dev/null || true
  sleep 2
  sudo pkill -9 -f "k3s server" 2>/dev/null || true
  sleep 1
  sudo rm -rf /var/lib/rancher/k3s /etc/rancher/k3s /run/k3s /var/lib/cni 2>/dev/null || true
  rm -f "${KUBECONFIG_PATH}"
  echo "Done."
}

install_k3s_if_missing() {
  if command -v k3s &>/dev/null; then
    echo "k3s already installed: $(k3s --version 2>&1 | head -1)"
    return
  fi
  echo "Installing k3s..."
  curl -sfL https://get.k3s.io | INSTALL_K3S_SKIP_START=true INSTALL_K3S_SKIP_ENABLE=true sh -
}

setup_cni() {
  sudo mkdir -p /etc/cni/net.d /opt/cni/bin

  # Bridge CNI with routes for k3s service CIDR (10.43.0.0/16).
  # Without the service route, pods can't reach ClusterIP addresses
  # (kube-proxy DNAT rules live on the host, but pods need a route
  # to send the packets there via the bridge gateway).
  sudo tee /etc/cni/net.d/10-bridge.conflist > /dev/null << 'CNIEOF'
{
  "cniVersion": "1.0.0",
  "name": "bridge",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "cni0",
      "isGateway": true,
      "ipMasq": false,
      "ipam": {
        "type": "host-local",
        "ranges": [[{"subnet": "10.85.0.0/16"}]],
        "routes": [
          {"dst": "10.43.0.0/16"},
          {"dst": "0.0.0.0/0"}
        ]
      }
    },
    {
      "type": "loopback"
    }
  ]
}
CNIEOF
}

link_cni_binaries() {
  local k3s_data_dir
  k3s_data_dir=$(find /var/lib/rancher/k3s/data -maxdepth 1 -mindepth 1 -type d ! -name current 2>/dev/null | head -1)
  if [ -z "${k3s_data_dir}" ]; then
    echo "Warning: k3s data dir not found yet, CNI binaries will be linked after start"
    return 1
  fi
  sudo cp "${k3s_data_dir}/bin/cni" /opt/cni/bin/cni
  for plugin in loopback bridge host-local portmap bandwidth; do
    sudo ln -sf cni "/opt/cni/bin/${plugin}"
  done
  return 0
}

start_k3s() {
  echo "Starting k3s..."
  sudo k3s server \
    --disable traefik \
    --disable servicelb \
    --disable-network-policy \
    --flannel-backend none \
    --write-kubeconfig-mode 644 \
    --snapshotter native \
    --kube-proxy-arg proxy-mode=nftables \
    &>"${K3S_LOG}" &

  echo "Waiting for k3s API server..."
  for i in $(seq 1 60); do
    if [ -f /etc/rancher/k3s/k3s.yaml ]; then
      mkdir -p "$(dirname "${KUBECONFIG_PATH}")"
      sudo cp /etc/rancher/k3s/k3s.yaml "${KUBECONFIG_PATH}"
      sudo chown "$(id -u):$(id -g)" "${KUBECONFIG_PATH}"

      # Link CNI binaries once k3s data dir exists
      link_cni_binaries 2>/dev/null || true

      if kubectl --kubeconfig="${KUBECONFIG_PATH}" get nodes 2>/dev/null | grep -q "Ready"; then
        echo "k3s ready after $((i * 2))s"
        return 0
      fi
    fi
    sleep 2
  done

  echo "ERROR: k3s failed to start within 120s. Log tail:"
  tail -20 "${K3S_LOG}"
  return 1
}

install_argocd() {
  echo "Installing ArgoCD..."
  export KUBECONFIG="${KUBECONFIG_PATH}"

  kubectl create namespace argocd 2>/dev/null || true
  kubectl apply -n argocd --server-side --force-conflicts \
    -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml \
    2>&1 | tail -3

  echo "Waiting for ArgoCD pods (this pulls ~200MB of images)..."
  for i in $(seq 1 90); do
    local ready
    ready=$(kubectl get pods -n argocd --no-headers 2>/dev/null | grep -c "Running" || echo 0)
    local total
    total=$(kubectl get pods -n argocd --no-headers 2>/dev/null | wc -l || echo 0)
    if [ "${ready}" -ge 5 ] && [ "${ready}" -eq "${total}" ]; then
      echo "ArgoCD ready (${ready}/${total} pods running) after $((i * 5))s"
      return 0
    fi
    if [ $((i % 6)) -eq 0 ]; then
      echo "  ... ${ready}/${total} pods running ($(( i * 5 ))s elapsed)"
    fi
    sleep 5
  done

  echo "WARNING: Not all ArgoCD pods are running yet. Current state:"
  kubectl get pods -n argocd
  return 1
}

setup_masquerade() {
  # Pods on the bridge network (10.85.0.0/16) need masquerade so return
  # packets from the API server (on the host network) find their way back.
  sudo nft add table ip atlas-masq 2>/dev/null || true
  sudo nft 'add chain ip atlas-masq postrouting { type nat hook postrouting priority srcnat; policy accept; }' 2>/dev/null || true
  sudo nft add rule ip atlas-masq postrouting ip saddr 10.85.0.0/16 masquerade 2>/dev/null || true
}

configure_argocd_cli() {
  export KUBECONFIG="${KUBECONFIG_PATH}"
  kubectl config set-context --current --namespace=argocd 2>/dev/null || true
}

# --- Main ---

if [ "${1:-}" = "teardown" ]; then
  teardown
  exit 0
fi

install_k3s_if_missing
setup_cni
start_k3s
link_cni_binaries
setup_masquerade
install_argocd
configure_argocd_cli

echo ""
echo "=== k3s + ArgoCD ready ==="
echo "KUBECONFIG=${KUBECONFIG_PATH}"
echo ""
echo "Quick checks:"
echo "  kubectl get nodes"
echo "  kubectl get pods -n argocd"
echo "  argocd appset generate --core <file>"
echo ""
echo "Teardown:"
echo "  .claude/utils/k3s-setup.sh teardown"
