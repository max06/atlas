#!/bin/sh
# Install pre-built CLI tools from GitHub releases.
# Usage: install-tools.sh yq=v4.53.2 dyff=v1.12.0
set -e

for spec in "$@"; do
  tool="${spec%%=*}"
  version="${spec#*=}"
  echo "Installing ${tool} ${version}..."

  case "$tool" in
    yq)
      curl -fsSL "https://github.com/mikefarah/yq/releases/download/${version}/yq_linux_amd64" \
        -o /usr/local/bin/yq
      chmod +x /usr/local/bin/yq
      ;;
    dyff)
      curl -fsSL "https://github.com/homeport/dyff/releases/download/${version}/dyff_${version#v}_linux_amd64.tar.gz" \
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
