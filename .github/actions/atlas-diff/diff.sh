#!/usr/bin/env bash
# ATLAS snapshot-review: generate per-release diffs between baseline and PR renders.
#
# Uses dyff for YAML-aware, Kubernetes-aware diffing. Handles release bucketing
# (added/removed/modified/suppressed) based on the redaction replay status.
#
# Required environment:
#   BASELINE_DIR     — path to baseline render tree
#   PR_DIR           — path to PR render tree
#   DIFF_TEMP        — temporary directory for scratch files (defaults to $RUNNER_TEMP)
#
# Optional environment:
#   SIDEDUMP_MAP_DIR — path to captured redaction maps from the PR render
#   REPLAY_STATUS_FILE — path to replay-status.txt from the replay step
#   GITHUB_OUTPUT    — output file for step outputs (default: /dev/null)
#
# Outputs (written to $GITHUB_OUTPUT):
#   status              — empty / no-changes / changes
#   total               — number of changed resources (when status=changes)
#   releases            — number of affected releases (when status=changes)
#   suppressed_nomap    — releases suppressed due to missing redaction maps
#   suppressed_removed  — releases suppressed because they were removed
#   truncated           — "true" if PR comment body was truncated
#
# Output files (written to $DIFF_TEMP):
#   comment-diff.md     — per-release diff blocks for PR comment
#   summary-diff.md     — per-release diff blocks for job summary (untruncated)
#   affected.md         — bullet list of affected releases

set -euo pipefail

BASELINE_DIR="${BASELINE_DIR:-}"
PR_DIR="${PR_DIR:-}"
DIFF_TEMP="${DIFF_TEMP:-${RUNNER_TEMP:-/tmp}}"
SIDEDUMP_MAP_DIR="${SIDEDUMP_MAP_DIR:-}"
REPLAY_STATUS_FILE="${REPLAY_STATUS_FILE:-${DIFF_TEMP}/replay-status.txt}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"

# ── Empty check ─────────────────────────────────────────────────────────────
HAS_BASELINE=false
HAS_PR=false
[ -d "$BASELINE_DIR" ] && [ -n "$(ls -A "$BASELINE_DIR" 2>/dev/null)" ] && HAS_BASELINE=true
[ -d "$PR_DIR" ] && [ -n "$(ls -A "$PR_DIR" 2>/dev/null)" ] && HAS_PR=true

if [ "$HAS_BASELINE" = false ] && [ "$HAS_PR" = false ]; then
  echo "status=empty" >> "$GITHUB_OUTPUT"
  exit 0
fi

# ── Load replay status ──────────────────────────────────────────────────────
# The replay step emits one line per release: "scrubbed <path>" or "no-map <path>".
# Releases in the no-map set had no PR-side redaction map — their baseline may
# contain unredacted secrets. However, if the map file exists but is empty ({}),
# that means the release has no secrets and is safe to diff.
declare -A NOMAP_SET=()
if [ -f "$REPLAY_STATUS_FILE" ]; then
  while IFS=' ' read -r status path; do
    if [ "$status" = "no-map" ]; then
      # Check if a map file exists but is empty (no secrets) vs truly missing.
      # An empty map ({}) means the release was processed by a current ATLAS
      # that supports sidedump — it just has no secrets. Safe to show full diff.
      map_file="${SIDEDUMP_MAP_DIR}/${path}.json"
      if [ -n "$SIDEDUMP_MAP_DIR" ] && [ -f "$map_file" ]; then
        MAP_SIZE=$(stat -c%s "$map_file" 2>/dev/null || echo 999)
        if [ "$MAP_SIZE" -le 3 ]; then
          # Empty map — no secrets, safe to diff
          continue
        fi
      fi
      NOMAP_SET["$path"]=1
    fi
  done < "$REPLAY_STATUS_FILE"
fi

# ── Release path discovery ──────────────────────────────────────────────────
# A release dir contains chart-name/templates/ — we find "templates" dirs and
# strip the chart+templates suffix to get <cluster>/<deployment>/<release>.
release_paths() {
  local root="$1"
  [ -d "$root" ] || return 0
  find "$root" -type d -name templates 2>/dev/null | \
    sed -E "s|^${root}/(.*)/[^/]+/templates\$|\1|" | sort -u
}

RELEASE_PATHS=$( { release_paths "$BASELINE_DIR"; release_paths "$PR_DIR"; } | sort -u )

# ── Walk releases and diff ──────────────────────────────────────────────────
HAS_CHANGES=false
COMMENT_BODY=""
SUMMARY_BODY=""
TOTAL_CHANGES=0
TOTAL_RELEASES=0
AFFECTED_DEPLOYMENTS=""
HAS_TRUNCATION=false
SUPPRESSED_NOMAP=0
SUPPRESSED_REMOVED=0

# Concatenate all YAML files under a release dir into a single multi-doc stream.
cat_yamls() {
  local dir="$1"
  if [ -d "$dir" ]; then
    find "$dir" -name '*.yaml' -type f 2>/dev/null | sort | xargs cat 2>/dev/null || true
  fi
}

for RELEASE_PATH in $RELEASE_PATHS; do
  RELEASE=$(basename "$RELEASE_PATH")
  REMAINDER=$(dirname "$RELEASE_PATH")
  DEPLOYMENT=$(basename "$REMAINDER")
  CLUSTER=$(dirname "$REMAINDER")
  RELEASE_HEADER="$CLUSTER / $DEPLOYMENT / $RELEASE"

  IN_BASELINE=false
  IN_PR=false
  [ -d "$BASELINE_DIR/$RELEASE_PATH" ] && IN_BASELINE=true
  [ -d "$PR_DIR/$RELEASE_PATH" ] && IN_PR=true

  # ── Bucket: suppressed releases ─────────────────────────────────────────
  SUPPRESS_REASON=""
  if [ "$IN_BASELINE" = true ] && [ "$IN_PR" = false ]; then
    SUPPRESS_REASON="removed"
    SUPPRESSED_REMOVED=$((SUPPRESSED_REMOVED + 1))
  elif [ "$IN_BASELINE" = true ] && [ "$IN_PR" = true ] \
       && [ -n "${NOMAP_SET[$RELEASE_PATH]:-}" ]; then
    # No usable redaction map. Skip if output is byte-identical (no leak risk).
    if diff -rq "$BASELINE_DIR/$RELEASE_PATH" "$PR_DIR/$RELEASE_PATH" >/dev/null 2>&1; then
      continue
    fi
    SUPPRESS_REASON="no-map"
    SUPPRESSED_NOMAP=$((SUPPRESSED_NOMAP + 1))
  fi

  if [ -n "$SUPPRESS_REASON" ]; then
    HAS_CHANGES=true
    TOTAL_RELEASES=$((TOTAL_RELEASES + 1))
    case "$SUPPRESS_REASON" in
      removed)
        SUMMARY_NOTE="release removed — diff suppressed to avoid leaking unredacted baseline output"
        ;;
      no-map)
        SUMMARY_NOTE="no PR-side redaction map — diff suppressed to avoid leaking template-level secrets from the pinned ATLAS version"
        ;;
    esac
    SUPPRESS_BLOCK="
<details>
<summary>${RELEASE_HEADER} (${SUPPRESS_REASON})</summary>

> ${SUMMARY_NOTE}

</details>
"
    COMMENT_BODY="${COMMENT_BODY}${SUPPRESS_BLOCK}"
    SUMMARY_BODY="${SUMMARY_BODY}${SUPPRESS_BLOCK}"
    AFFECTED_DEPLOYMENTS="$(printf '%s\n%s' "$AFFECTED_DEPLOYMENTS" "- **${RELEASE_HEADER}** (${SUPPRESS_REASON})")"
    continue
  fi

  # ── Bucket: diff with dyff ─────────────────────────────────────────────
  BASE_CONCAT="${DIFF_TEMP}/dyff-base-${RELEASE//\//_}.yaml"
  PR_CONCAT="${DIFF_TEMP}/dyff-pr-${RELEASE//\//_}.yaml"

  cat_yamls "$BASELINE_DIR/$RELEASE_PATH" > "$BASE_CONCAT"
  cat_yamls "$PR_DIR/$RELEASE_PATH" > "$PR_CONCAT"

  # dyff between with GitHub markdown output. --set-exit-code returns 1 when
  # differences are found (0 = identical).
  RELEASE_DIFF=""
  if ! RELEASE_DIFF=$(dyff between "$BASE_CONCAT" "$PR_CONCAT" \
    --output github --detect-kubernetes --omit-header --set-exit-code 2>/dev/null); then
    # Exit code 1 = differences found (not an error)
    true
  fi

  [ -z "$RELEASE_DIFF" ] && continue

  HAS_CHANGES=true
  TOTAL_RELEASES=$((TOTAL_RELEASES + 1))
  TOTAL_CHANGES=$((TOTAL_CHANGES + 1))

  # Determine change type label
  if [ "$IN_BASELINE" = false ]; then
    TYPE_LABEL=" (new)"
  elif [ "$IN_PR" = false ]; then
    TYPE_LABEL=" (removed)"
  else
    TYPE_LABEL=""
  fi

  DIFF_LINES=$(echo "$RELEASE_DIFF" | wc -l)
  AFFECTED_DEPLOYMENTS="$(printf '%s\n%s' "$AFFECTED_DEPLOYMENTS" "- **${RELEASE_HEADER}**${TYPE_LABEL} (${DIFF_LINES} lines)")"

  # Full diff block (used in both comment and summary)
  DIFF_BLOCK="
<details>
<summary>${RELEASE_HEADER}${TYPE_LABEL} (${DIFF_LINES} lines)</summary>

\`\`\`
${RELEASE_DIFF}
\`\`\`

</details>
"

  # Job summary always gets full output
  SUMMARY_BODY="${SUMMARY_BODY}${DIFF_BLOCK}"

  # PR comment — truncate if exceeding 55KB
  CURRENT_SIZE=$(( ${#COMMENT_BODY} ))
  DIFF_SIZE=${#RELEASE_DIFF}
  if [ $((CURRENT_SIZE + DIFF_SIZE)) -gt 55000 ]; then
    HAS_TRUNCATION=true
    COMMENT_BODY="${COMMENT_BODY}
<details>
<summary>${RELEASE_HEADER}${TYPE_LABEL} (${DIFF_LINES} lines, truncated)</summary>

> Full diff available in the [job summary].

</details>
"
  else
    COMMENT_BODY="${COMMENT_BODY}${DIFF_BLOCK}"
  fi
done

# ── Write outputs ─────────────────────────────────────────────────────────
if [ "$HAS_CHANGES" = true ]; then
  echo "status=changes" >> "$GITHUB_OUTPUT"
  echo "total=$TOTAL_CHANGES" >> "$GITHUB_OUTPUT"
  echo "releases=$TOTAL_RELEASES" >> "$GITHUB_OUTPUT"
  echo "suppressed_nomap=$SUPPRESSED_NOMAP" >> "$GITHUB_OUTPUT"
  echo "suppressed_removed=$SUPPRESSED_REMOVED" >> "$GITHUB_OUTPUT"

  echo "$COMMENT_BODY" > "${DIFF_TEMP}/comment-diff.md"
  echo "$AFFECTED_DEPLOYMENTS" > "${DIFF_TEMP}/affected.md"
  echo "$SUMMARY_BODY" > "${DIFF_TEMP}/summary-diff.md"

  if [ "$HAS_TRUNCATION" = true ]; then
    echo "truncated=true" >> "$GITHUB_OUTPUT"
  fi
else
  echo "status=no-changes" >> "$GITHUB_OUTPUT"
fi
