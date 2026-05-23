#!/bin/bash
# Entrypoint for the atlas-render Docker action.
# Maps action inputs (INPUT_*) to the environment variables render.sh expects.
set -euo pipefail

# Make the atlas-redact post-renderer available as a Helm plugin.
# The plugin is baked into the image at /atlas-plugins/atlas-redact/.
DEFAULT_PLUGINS=$(helm env HELM_PLUGINS 2>/dev/null || echo "")
export HELM_PLUGINS="${DEFAULT_PLUGINS:+${DEFAULT_PLUGINS}:}/atlas-plugins"

export HELMFILE_PATH="${INPUT_HELMFILE_PATH:?helmfile-path input is required}"
export SNAPSHOT_LABEL="${INPUT_SNAPSHOT_LABEL:?snapshot-label input is required}"
export RENDER_TEMP="${RUNNER_TEMP:-/tmp}"
export BLOCKING="${INPUT_BLOCKING:-true}"

if [ -n "${INPUT_SOPS_AGE_KEY:-}" ]; then
  export SOPS_AGE_KEY="${INPUT_SOPS_AGE_KEY}"
fi

if [ "${INPUT_ENABLE_SIDEDUMP:-false}" = "true" ]; then
  export ATLAS_SIDEDUMP_MAP_DIR="${RENDER_TEMP}/sidedump-maps"
  mkdir -p "$ATLAS_SIDEDUMP_MAP_DIR"
fi

if [ "${INPUT_MERGE_FALLBACK:-}" = "true" ]; then
  export MERGE_FALLBACK="true"
fi

exec /render.sh
