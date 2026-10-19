#!/bin/sh
# Install pre-built CLI tools from GitHub releases.
# Usage: install-tools.sh helm=v4.1.4 helmfile=v1.5.0 sops=v3.12.2 yq=v4.53.2
#
# Each argument is tool=version. The script downloads the linux binary for
# the target architecture (TARGETARCH from docker buildx, else the machine's:
# amd64 or arm64), extracts it if needed, and places it in /usr/local/bin.
set -e

arch="${TARGETARCH:-}"
if [ -z "$arch" ]; then
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
fi

for spec in "$@"; do
  tool="${spec%%=*}"
  version="${spec#*=}"
  echo "Installing ${tool} ${version}..."

  case "$tool" in
    helm)
      curl -fsSL "https://get.helm.sh/helm-${version}-linux-${arch}.tar.gz" \
        | tar xz -C /tmp
      mv /tmp/linux-${arch}/helm /usr/local/bin/helm
      rm -rf /tmp/linux-${arch}
      ;;
    helmfile)
      curl -fsSL "https://github.com/helmfile/helmfile/releases/download/${version}/helmfile_${version#v}_linux_${arch}.tar.gz" \
        | tar xz -C /tmp
      mv /tmp/helmfile /usr/local/bin/helmfile
      ;;
    sops)
      curl -fsSL "https://github.com/getsops/sops/releases/download/${version}/sops-${version}.linux.${arch}" \
        -o /usr/local/bin/sops
      chmod +x /usr/local/bin/sops
      ;;
    yq)
      curl -fsSL "https://github.com/mikefarah/yq/releases/download/${version}/yq_linux_${arch}" \
        -o /usr/local/bin/yq
      chmod +x /usr/local/bin/yq
      ;;
    kustomize)
      curl -fsSL "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2F${version}/kustomize_${version}_linux_${arch}.tar.gz" \
        | tar xz -C /tmp
      mv /tmp/kustomize /usr/local/bin/kustomize
      ;;
    *)
      echo "Unknown tool: ${tool}" >&2
      exit 1
      ;;
  esac

  echo "  ${tool} ${version} installed."
done
