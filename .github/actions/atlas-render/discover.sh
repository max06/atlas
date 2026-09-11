#!/usr/bin/env bash
# ATLAS snapshot-review: emit the discovery map of the checked-out tree.
#
# Runs the consumer's helmfile entry in ATLAS_DISCOVERY_MAP mode, which makes
# ATLAS print what the tree renders — every (cluster, deployment) pair, its
# deployment.yaml path and the app templates it uses — after ONE discovery
# pass (no release states, no values loader, no SOPS). classify.sh consumes
# two of these (target branch + merge result) to pick the render subset.
#
# Usage: discover.sh <output.json>
#
# Environment:
#   HELMFILE_PATH   — path to the helmfile entry point (required)
#
# Exit codes:
#   0  map written to <output.json>
#   2  the tree has no map — entry file missing, or the pinned ATLAS predates
#      discovery-map mode (its build output carries no atlasDiscovery). The
#      caller falls back to a full render; nothing is written.
#   1  helmfile itself failed (the tree does not build) — stderr is printed.
#      The caller treats it like a render error of that side.
set -euo pipefail

OUT="${1:?usage: discover.sh <output.json>}"
HELMFILE_PATH="${HELMFILE_PATH:?HELMFILE_PATH is required}"

if [ ! -f "$HELMFILE_PATH" ]; then
  echo "discover: helmfile not found at $HELMFILE_PATH" >&2
  exit 2
fi

BUILD_ERR="$(mktemp)"
trap 'rm -f "$BUILD_ERR"' EXIT

# `helmfile build` prints every state; the map is the rendered values of the
# release-less carrier state. yq only turns the YAML stream into a JSON array,
# the shaping happens in jq.
if ! MAP_JSON="$(ATLAS_DISCOVERY_MAP=1 helmfile -f "$HELMFILE_PATH" build --allow-no-matching-release 2>"$BUILD_ERR" \
    | yq eval-all -o=json '[.]' - \
    | jq -c '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0] // empty')"; then
  echo "discover: helmfile build failed" >&2
  cat "$BUILD_ERR" >&2
  exit 1
fi

if [ -z "$MAP_JSON" ]; then
  echo "discover: no discovery map in build output (ATLAS without ATLAS_DISCOVERY_MAP support)" >&2
  exit 2
fi

printf '%s\n' "$MAP_JSON" > "$OUT"
echo "discover: $(jq '.pairs | length' "$OUT") pairs → $OUT"
