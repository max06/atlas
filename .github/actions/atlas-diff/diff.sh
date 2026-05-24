#!/usr/bin/env bash
# ATLAS snapshot-review: generate per-release diffs between baseline and PR renders.
#
# Diffs individual resources within each release, producing nested collapsible
# sections: outer per-release, inner per-resource. Supports two diff modes:
#   dyff    — YAML-aware, Kubernetes-aware semantic diff (default)
#   classic — standard unified diff
#
# Handles release bucketing (added/removed/modified/suppressed) based on the
# redaction replay status.
#
# Required environment:
#   BASELINE_DIR     — path to baseline render tree
#   PR_DIR           — path to PR render tree
#   DIFF_TEMP        — temporary directory for scratch files (defaults to $RUNNER_TEMP)
#
# Optional environment:
#   DIFF_MODE          — "dyff" (default) or "classic"
#   SIDEDUMP_MAP_DIR   — path to captured redaction maps from the PR render
#   REPLAY_STATUS_FILE — path to replay-status.txt from the replay step
#   GITHUB_OUTPUT      — output file for step outputs (default: /dev/null)
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
DIFF_MODE="${DIFF_MODE:-dyff}"
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

# ── Resource helpers ────────────────────────────────────────────────────────

# Discover individual resource YAML files under a release dir.
# Returns paths relative to the release dir, sorted for stable pairing.
resource_files() {
  local dir="$1"
  [ -d "$dir" ] || return 0
  find "$dir" -name '*.yaml' -type f 2>/dev/null | sort
}

# Extract a human-readable resource label from YAML content: "Kind/name".
# Falls back to the filename if parsing fails.
resource_label() {
  local file="$1" filename kind name
  filename=$(basename "$file" .yaml)
  kind=$(grep -m1 '^kind:' "$file" 2>/dev/null | sed 's/^kind:[[:space:]]*//' || true)
  name=$(grep -m1 '^  name:' "$file" 2>/dev/null | sed 's/^  name:[[:space:]]*//' || true)
  if [ -n "$kind" ] && [ -n "$name" ]; then
    echo "${kind}/${name}"
  else
    echo "$filename"
  fi
}

# Diff a single resource file pair. Outputs the diff text (empty if identical).
# Classic mode uses --label to show "baseline" / "current" instead of absolute paths.
diff_resource() {
  local base_file="$1" pr_file="$2" output=""
  if [ "$DIFF_MODE" = "classic" ]; then
    output=$(diff -u --label baseline --label current "$base_file" "$pr_file" 2>/dev/null || true)
  else
    # dyff: exit code 1 = differences found (not an error)
    if ! output=$(dyff between "$base_file" "$pr_file" \
      --output github --detect-kubernetes --omit-header --set-exit-code 2>/dev/null); then
      true
    fi
  fi
  echo "$output"
}

# Show the full content of a file as an "added" or "removed" block.
diff_resource_full() {
  local file="$1" direction="$2" content prefix
  content=$(cat "$file")
  if [ "$DIFF_MODE" = "classic" ]; then
    if [ "$direction" = "added" ]; then
      prefix="+"
    else
      prefix="-"
    fi
    echo "$content" | sed "s/^/${prefix} /"
  else
    if [ "$direction" = "added" ]; then
      prefix="+ "
    else
      prefix="- "
    fi
    echo "! ${prefix}entire resource ${direction}:"
    echo "$content" | sed "s/^/${prefix}/"
  fi
}

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

  # ── Per-resource diffing ────────────────────────────────────────────────
  # Collect all resource files from both sides, paired by their path relative
  # to the release dir. Each resource gets its own inner <details> section.
  BASE_DIR="$BASELINE_DIR/$RELEASE_PATH"
  HEAD_DIR="$PR_DIR/$RELEASE_PATH"

  # Build a union of relative resource paths from both sides
  RESOURCE_RELPATHS=""
  if [ -d "$BASE_DIR" ]; then
    RESOURCE_RELPATHS=$(find "$BASE_DIR" -name '*.yaml' -type f 2>/dev/null \
      | sed "s|^${BASE_DIR}/||" | sort)
  fi
  if [ -d "$HEAD_DIR" ]; then
    PR_RELPATHS=$(find "$HEAD_DIR" -name '*.yaml' -type f 2>/dev/null \
      | sed "s|^${HEAD_DIR}/||" | sort)
    RESOURCE_RELPATHS=$(printf '%s\n%s' "$RESOURCE_RELPATHS" "$PR_RELPATHS" | sort -u)
  fi
  # Filter empty lines
  RESOURCE_RELPATHS=$(echo "$RESOURCE_RELPATHS" | grep -v '^$' || true)

  [ -z "$RESOURCE_RELPATHS" ] && continue

  RELEASE_RESOURCE_BLOCKS=""
  RELEASE_RESOURCE_COUNT=0
  RELEASE_TOTAL_LINES=0

  while IFS= read -r REL_FILE; do
    BASE_FILE="$BASE_DIR/$REL_FILE"
    PR_FILE="$HEAD_DIR/$REL_FILE"
    HAS_BASE=false
    HAS_PR=false
    [ -f "$BASE_FILE" ] && HAS_BASE=true
    [ -f "$PR_FILE" ] && HAS_PR=true

    # Determine resource label from whichever side exists
    if [ "$HAS_PR" = true ]; then
      RES_LABEL=$(resource_label "$PR_FILE")
    else
      RES_LABEL=$(resource_label "$BASE_FILE")
    fi

    RES_DIFF=""
    RES_TYPE=""
    if [ "$HAS_BASE" = true ] && [ "$HAS_PR" = true ]; then
      RES_DIFF=$(diff_resource "$BASE_FILE" "$PR_FILE")
      [ -z "$RES_DIFF" ] && continue
    elif [ "$HAS_BASE" = false ] && [ "$HAS_PR" = true ]; then
      RES_DIFF=$(diff_resource_full "$PR_FILE" "added")
      RES_TYPE=" (new)"
    elif [ "$HAS_BASE" = true ] && [ "$HAS_PR" = false ]; then
      RES_DIFF=$(diff_resource_full "$BASE_FILE" "removed")
      RES_TYPE=" (removed)"
    fi

    [ -z "$RES_DIFF" ] && continue

    RES_LINES=$(echo "$RES_DIFF" | wc -l)
    RELEASE_RESOURCE_COUNT=$((RELEASE_RESOURCE_COUNT + 1))
    RELEASE_TOTAL_LINES=$((RELEASE_TOTAL_LINES + RES_LINES))

    RELEASE_RESOURCE_BLOCKS="${RELEASE_RESOURCE_BLOCKS}
<details>
<summary>${RES_LABEL}${RES_TYPE} (${RES_LINES} lines)</summary>

\`\`\`diff
${RES_DIFF}
\`\`\`

</details>
"
  done <<< "$RESOURCE_RELPATHS"

  [ "$RELEASE_RESOURCE_COUNT" -eq 0 ] && continue

  HAS_CHANGES=true
  TOTAL_RELEASES=$((TOTAL_RELEASES + 1))
  TOTAL_CHANGES=$((TOTAL_CHANGES + RELEASE_RESOURCE_COUNT))

  # Determine release-level change type label
  if [ "$IN_BASELINE" = false ]; then
    TYPE_LABEL=" (new)"
  elif [ "$IN_PR" = false ]; then
    TYPE_LABEL=" (removed)"
  else
    TYPE_LABEL=""
  fi

  AFFECTED_DEPLOYMENTS="$(printf '%s\n%s' "$AFFECTED_DEPLOYMENTS" "- **${RELEASE_HEADER}**${TYPE_LABEL} (${RELEASE_RESOURCE_COUNT} resources)")"

  # Outer release block wrapping the inner per-resource blocks
  DIFF_BLOCK="
<details>
<summary>${RELEASE_HEADER}${TYPE_LABEL} (${RELEASE_RESOURCE_COUNT} resources)</summary>
${RELEASE_RESOURCE_BLOCKS}
</details>
"

  # Job summary always gets full output
  SUMMARY_BODY="${SUMMARY_BODY}${DIFF_BLOCK}"

  # PR comment — truncate if exceeding 55KB
  CURRENT_SIZE=${#COMMENT_BODY}
  BLOCK_SIZE=${#DIFF_BLOCK}
  if [ $((CURRENT_SIZE + BLOCK_SIZE)) -gt 55000 ]; then
    HAS_TRUNCATION=true
    COMMENT_BODY="${COMMENT_BODY}
<details>
<summary>${RELEASE_HEADER}${TYPE_LABEL} (${RELEASE_RESOURCE_COUNT} resources, truncated)</summary>

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
