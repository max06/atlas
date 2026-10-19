#!/bin/sh
# Install pre-built CLI tools from GitHub releases.
# Usage: install-tools.sh yq=v4.53.2 dyff=v1.12.0
# The binaries match TARGETARCH (docker buildx) or the machine: amd64 or arm64.
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
    yq)
      curl -fsSL "https://github.com/mikefarah/yq/releases/download/${version}/yq_linux_${arch}" \
        -o /usr/local/bin/yq
      chmod +x /usr/local/bin/yq
      ;;
    dyff)
      curl -fsSL "https://github.com/homeport/dyff/releases/download/${version}/dyff_${version#v}_linux_${arch}.tar.gz" \
        | tar xz -C /tmp
      mv /tmp/dyff /usr/local/bin/dyff
      ;;
    *)
      echo "Unknown tool: ${tool}" >&2
      exit 1
      ;;
  esac

  echo "  ${tool} ${version} installed."
done
