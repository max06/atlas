#!/bin/sh
# Install pre-built CLI tools from GitHub releases.
# Usage: install-tools.sh helm=v4.1.4 helmfile=v1.5.0 sops=v3.12.2 yq=v4.53.2
#
# Each argument is tool=version. The script downloads the correct binary for
# linux/amd64, extracts it if needed, and places it in /usr/local/bin.
set -e

for spec in "$@"; do
  tool="${spec%%=*}"
  version="${spec#*=}"
  echo "Installing ${tool} ${version}..."

  case "$tool" in
    helm)
      curl -fsSL "https://get.helm.sh/helm-${version}-linux-amd64.tar.gz" \
        | tar xz -C /tmp
      mv /tmp/linux-amd64/helm /usr/local/bin/helm
      rm -rf /tmp/linux-amd64
      ;;
    helmfile)
      curl -fsSL "https://github.com/helmfile/helmfile/releases/download/${version}/helmfile_${version#v}_linux_amd64.tar.gz" \
        | tar xz -C /tmp
      mv /tmp/helmfile /usr/local/bin/helmfile
      ;;
    sops)
      curl -fsSL "https://github.com/getsops/sops/releases/download/${version}/sops-${version}.linux.amd64" \
        -o /usr/local/bin/sops
      chmod +x /usr/local/bin/sops
      ;;
    yq)
      curl -fsSL "https://github.com/mikefarah/yq/releases/download/${version}/yq_linux_amd64" \
        -o /usr/local/bin/yq
      chmod +x /usr/local/bin/yq
      ;;
    kustomize)
      curl -fsSL "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2F${version}/kustomize_${version}_linux_amd64.tar.gz" \
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
