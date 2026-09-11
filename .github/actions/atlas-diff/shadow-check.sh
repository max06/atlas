#!/usr/bin/env bash
# ATLAS snapshot-review: shadow check for changed-file render subsetting.
#
# In shadow mode the review still renders EVERYTHING, but the classifier's
# selection is computed alongside. This script asserts the property the cutover
# depends on: every release the full diff reports as changed belongs to a
# (cluster, deployment) pair the classifier selected. A violation is a false
# negative — a change the subset render would have missed — and must surface
# loudly while it is still harmless.
#
# Required environment:
#   AFFECTED_PATHS_FILE — diff.sh's affected-paths.txt ("<cluster>/<deployment>/<release>")
#   CLASSIFY_JSON       — classify.sh's classify.json
#
# Optional environment:
#   GITHUB_OUTPUT       — step outputs (default: /dev/null)
#   SHADOW_OUT          — where to write shadow-check.md (default: dirname of CLASSIFY_JSON)
#
# Outputs (GITHUB_OUTPUT):
#   shadow_result   — covered / uncovered / full (classifier chose full render → nothing to check)
#   shadow_missed   — number of affected releases outside the selection
set -euo pipefail

AFFECTED_PATHS_FILE="${AFFECTED_PATHS_FILE:?}"
CLASSIFY_JSON="${CLASSIFY_JSON:?}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"
SHADOW_OUT="${SHADOW_OUT:-$(dirname "$CLASSIFY_JSON")}"
mkdir -p "$SHADOW_OUT"

MODE="$(jq -r .mode "$CLASSIFY_JSON")"
if [ "$MODE" != "subset" ]; then
  echo "shadow_result=full" >> "$GITHUB_OUTPUT"
  echo "shadow_missed=0" >> "$GITHUB_OUTPUT"
  echo "**Subset shadow check:** not applicable (classifier chose a full render)." > "$SHADOW_OUT/shadow-check.md"
  exit 0
fi

# affected "<cluster>/<deployment>/<release>" → "<cluster>|<deployment>"
# (cluster paths may contain "/", so split from the right: last = release,
# second to last = deployment, rest = cluster)
MISSED="$(awk -F'/' 'NF>=3 { rel=$NF; dep=$(NF-1); c=$1; for (i=2;i<=NF-2;i++) c=c "/" $i; print c "|" dep }' "$AFFECTED_PATHS_FILE" \
  | sort -u \
  | grep -vxF -f <(jq -r '.selected[]' "$CLASSIFY_JSON"; echo "__none__") || true)"

if [ -z "$MISSED" ]; then
  echo "shadow_result=covered" >> "$GITHUB_OUTPUT"
  echo "shadow_missed=0" >> "$GITHUB_OUTPUT"
  echo "**Subset shadow check:** every changed release is inside the classifier's selection ($(jq '.selected | length' "$CLASSIFY_JSON") pairs selected)." > "$SHADOW_OUT/shadow-check.md"
  echo "shadow-check: covered"
else
  COUNT="$(wc -l <<< "$MISSED")"
  echo "shadow_result=uncovered" >> "$GITHUB_OUTPUT"
  echo "shadow_missed=$COUNT" >> "$GITHUB_OUTPUT"
  {
    echo "> [!WARNING]"
    echo "> **Subset shadow check FAILED:** ${COUNT} changed deployment(s) lie outside the classifier's selection. A subset render would have missed them — the full render above is authoritative. Please report this path/rule combination."
    echo ">"
    while IFS= read -r m; do echo "> - \`${m%%|*}\` / \`${m#*|}\`"; done <<< "$MISSED"
  } > "$SHADOW_OUT/shadow-check.md"
  echo "::warning::subset shadow check: ${COUNT} changed deployment(s) outside the classifier's selection"
  printf '%s\n' "$MISSED"
fi
