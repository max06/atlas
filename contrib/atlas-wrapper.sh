#!/usr/bin/env bash
# atlas — runs the atlas CLI from its container image.
#
# Copy this file into a consumer repository as bin/atlas. It needs docker and
# git, nothing else: helm, helmfile, kustomize, sops and the CLI come with the
# image, in the versions the CLI is tested with.
#
# Image version, first match wins:
#   1. ATLAS_IMAGE                  full image reference, e.g. to test a branch build
#   2. atlas.yaml `spec.engine:`    at the repository root (the engine pin)
#   3. ATLAS_DEFAULT_TAG below
#
# The repository is mounted at its own absolute path and the CLI runs in the
# caller's directory: rendered values embed absolute paths, so host and
# container renders stay identical. The caller's uid owns everything written.
# A named volume (atlas-cache) keeps charts and helmfile states between runs.
#
# Secrets: nothing is decrypted unless --decrypt, --redact or --argocd is
# given. Only then the wrapper passes the SOPS key: SOPS_AGE_KEY_FILE, mounted
# read-only. PGP keys (gpg-agent) are not forwarded into the container.
set -euo pipefail

ATLAS_DEFAULT_TAG="dev"
ATLAS_REPOSITORY="ghcr.io/max06/atlas"

command -v docker >/dev/null || { echo "atlas: docker not found" >&2; exit 127; }

repo="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "atlas: run inside a git repository" >&2
  exit 2
}

image="${ATLAS_IMAGE:-}"
if [ -z "$image" ]; then
  tag=""
  if [ -f "$repo/atlas.yaml" ]; then
    # spec.engine is the only "engine:" key in the file; atlas validates the rest.
    tag="$(sed -n 's/^[[:space:]]\{1,\}engine:[[:space:]]*["'\'']\{0,1\}\([^"'\''[:space:]#]*\).*/\1/p' "$repo/atlas.yaml" | head -n1)"
  fi
  image="$ATLAS_REPOSITORY:${tag:-$ATLAS_DEFAULT_TAG}"
fi

args=(
  --rm --init
  --user "$(id -u):$(id -g)"
  --volume "$repo:$repo"
  --volume "atlas-cache:/cache"
  --workdir "$PWD"
)

# A linked worktree's .git is a file pointing into the main clone: mount the
# common git dir as well, or git inside the container cannot read the history.
common="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir)"
case "$common" in
  "$repo"/*) ;;
  *) args+=(--volume "$common:$common") ;;
esac

[ -t 0 ] && [ -t 1 ] && args+=(--tty)
[ -t 0 ] || args+=(--interactive)

decrypt=false
for a in "$@"; do
  case "$a" in
    --decrypt | --redact | --argocd) decrypt=true ;;
  esac
done
if $decrypt; then
  if [ -n "${SOPS_AGE_KEY_FILE:-}" ] && [ -f "$SOPS_AGE_KEY_FILE" ]; then
    args+=(--volume "$SOPS_AGE_KEY_FILE:/run/atlas/age-key:ro" --env SOPS_AGE_KEY_FILE=/run/atlas/age-key)
  else
    echo "atlas: --decrypt needs SOPS_AGE_KEY_FILE (age key); PGP keys are not forwarded into the container" >&2
  fi
fi

# Pull explicitly when the image is not local: a branch that pins an image
# without a build for this machine's architecture (an older tag, before the
# multi-arch images) fails here with "no matching manifest". Say what to do
# instead of letting helmfile crash under emulation.
if ! docker image inspect "$image" >/dev/null 2>&1; then
  if ! pull_output="$(docker pull -q "$image" 2>&1)"; then
    echo "$pull_output" >&2
    if grep -qi 'no matching manifest' <<<"$pull_output"; then
      echo "atlas: the image $image has no build for this machine ($(uname -m)). Your branch pins an older atlas image; merge or rebase onto the default branch to get the current pin, or set ATLAS_IMAGE." >&2
    fi
    exit 125
  fi
fi

exec docker run "${args[@]}" "$image" "$@"
