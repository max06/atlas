#!/usr/bin/env bash
# Test shim: the classify.sh contract served by the atlas CLI (ATLAS_REVIEW_BACKEND=cli).
set -euo pipefail
: "${CLASSIFY_OUT:?}" "${MAP_BASELINE:?}" "${MAP_PR:?}" "${HELMFILE_PATH:?}" "${CHANGES_FILE:?}"
exec "${ATLAS_CLI:?ATLAS_CLI is required}" review classify -f "$HELMFILE_PATH" \
  --map-base "$MAP_BASELINE" --map-head "$MAP_PR" --changes-file "$CHANGES_FILE" \
  --out "$CLASSIFY_OUT" --github-output "${GITHUB_OUTPUT:-/dev/null}" >/dev/null
