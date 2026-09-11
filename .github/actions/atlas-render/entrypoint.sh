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
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"

case "${INPUT_MODE:-render}" in
  discover)
    # Emit the discovery map of the checked-out tree (see discover.sh).
    MAP_FILE="${RENDER_TEMP}/${SNAPSHOT_LABEL}-map.json"
    set +e
    /discover.sh "$MAP_FILE"
    rc=$?
    set -e
    case "$rc" in
      0) echo "map_status=ok" >> "$GITHUB_OUTPUT"; echo "map_file=$MAP_FILE" >> "$GITHUB_OUTPUT" ;;
      2) echo "map_status=unsupported" >> "$GITHUB_OUTPUT"; echo "map_file=" >> "$GITHUB_OUTPUT"
         echo "::notice::No discovery map on the ${SNAPSHOT_LABEL} side — the pinned ATLAS predates ATLAS_DISCOVERY_MAP; falling back to a full render." ;;
      *) echo "map_status=error" >> "$GITHUB_OUTPUT"; echo "map_file=" >> "$GITHUB_OUTPUT"
         echo "::warning::Discovery map failed on the ${SNAPSHOT_LABEL} side — falling back to a full render (the render step reports the actual error)." ;;
    esac
    exit 0
    ;;
  classify)
    export CLASSIFY_OUT="${RENDER_TEMP}/classify"
    export MAP_BASELINE="${INPUT_MAP_BASELINE:-/nonexistent}"
    export MAP_PR="${INPUT_MAP_PR:-/nonexistent}"
    export CHANGES_FILE="${INPUT_CHANGES_FILE:-/nonexistent}"
    if [ ! -f "$CHANGES_FILE" ]; then
      # No changed-file list = nothing to classify → default-deny.
      mkdir -p "$CLASSIFY_OUT"
      : > "$CLASSIFY_OUT/changes.txt"
      : > "$CLASSIFY_OUT/pairs-baseline.txt"; : > "$CLASSIFY_OUT/pairs-pr.txt"
      echo '{"mode":"full","reason":"changed-file list unavailable","changes":[],"selected":[]}' > "$CLASSIFY_OUT/classify.json"
      echo "**Render scope:** full render — changed-file list unavailable." > "$CLASSIFY_OUT/classify-summary.md"
      { echo "mode=full"; echo "reason=changed-file list unavailable"; echo "selected_pr=0"; echo "total_pr=0"
        echo "pairs_baseline_file=$CLASSIFY_OUT/pairs-baseline.txt"; echo "pairs_pr_file=$CLASSIFY_OUT/pairs-pr.txt"; } >> "$GITHUB_OUTPUT"
    else
      /classify.sh
    fi
    echo "classify_dir=$CLASSIFY_OUT" >> "$GITHUB_OUTPUT"
    exit 0
    ;;
  render) ;;
  *) echo "::error::unknown mode '${INPUT_MODE}'"; exit 1 ;;
esac

if [ -n "${INPUT_PAIRS_FILE:-}" ]; then
  export RENDER_PAIRS_FILE="${INPUT_PAIRS_FILE}"
fi

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
