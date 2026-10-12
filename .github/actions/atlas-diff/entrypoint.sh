#!/bin/bash
# Entrypoint for the atlas-diff Docker action.
# Runs the three stages in order: replay → diff → comment.
set -euo pipefail

DIFF_TEMP="${RUNNER_TEMP:-/tmp}"

# ── Stage 1: Replay redaction maps over baseline ───────────────────────────
export BASELINE_DIR="${INPUT_BASELINE_DIR:-}"
export SIDEDUMP_MAP_DIR="${INPUT_SIDEDUMP_MAP_DIR:-}"
export REPLAY_TEMP="$DIFF_TEMP"
export SCRUB_SCRIPT="${INPUT_SCRUB_SCRIPT:-}"

# The scrub script may have been fetched to ATLAS_PLUGIN_DIR by the workflow
if [ -z "$SCRUB_SCRIPT" ] && [ -n "${ATLAS_PLUGIN_DIR:-}" ]; then
  SCRUB_SCRIPT="${ATLAS_PLUGIN_DIR}/scrub-baseline.sh"
fi
export SCRUB_SCRIPT

REPLAY_OUTPUT="${DIFF_TEMP}/replay-github-output"
: > "$REPLAY_OUTPUT"
GITHUB_OUTPUT="$REPLAY_OUTPUT" /replay.sh

# ── Stage 2: Diff ──────────────────────────────────────────────────────────
export PR_DIR="${INPUT_PR_DIR:-}"
export DIFF_TEMP
export DIFF_MODE="${INPUT_DIFF_MODE:-dyff}"
export REPLAY_STATUS_FILE="${DIFF_TEMP}/replay-status.txt"

DIFF_OUTPUT="${DIFF_TEMP}/diff-github-output"
: > "$DIFF_OUTPUT"
GITHUB_OUTPUT="$DIFF_OUTPUT" /diff.sh

# ── Stage 2b: Render-scope fragment + shadow check ─────────────────────────
# Builds $DIFF_TEMP/scope.md for comment.sh from the classifier's output.
#   off    → nothing
#   shadow → "full render" line + the classifier's would-be selection, plus
#            the shadow check (every changed release inside the selection?)
#   on     → the classifier's scope line as is (it already states a full
#            render when it chose one)
SUBSET_MODE="${INPUT_SUBSET_MODE:-off}"
CLASSIFY_DIR="${INPUT_CLASSIFY_DIR:-}"
RENDER_MODE="${INPUT_RENDER_MODE:-full}"
rm -f "${DIFF_TEMP}/scope.md"
if [ "$SUBSET_MODE" != "off" ] && [ -n "$CLASSIFY_DIR" ] && [ -f "$CLASSIFY_DIR/classify.json" ]; then
  SHADOW_OUTPUT="${DIFF_TEMP}/shadow-github-output"
  : > "$SHADOW_OUTPUT"
  if [ "$RENDER_MODE" = "full" ] && [ -f "${DIFF_TEMP}/affected-paths.txt" ]; then
    AFFECTED_PATHS_FILE="${DIFF_TEMP}/affected-paths.txt" CLASSIFY_JSON="$CLASSIFY_DIR/classify.json" \
      SHADOW_OUT="$DIFF_TEMP" GITHUB_OUTPUT="$SHADOW_OUTPUT" /shadow-check.sh || true
  else
    echo "shadow_result=skipped" >> "$SHADOW_OUTPUT"
    echo "shadow_missed=0" >> "$SHADOW_OUTPUT"
  fi
  {
    if [ "$SUBSET_MODE" = "shadow" ]; then
      echo "**Render scope:** full render (changed-file subsetting runs in shadow mode)."
      if [ "$(jq -r .mode "$CLASSIFY_DIR/classify.json")" = "subset" ]; then
        echo
        echo "<details>"
        echo "<summary>Shadow: the classifier would have rendered $(jq '.selected_pr | length' "$CLASSIFY_DIR/classify.json") of $(jq '.pr_total' "$CLASSIFY_DIR/classify.json") deployments</summary>"
        echo
        sed '1,2d' "$CLASSIFY_DIR/classify-summary.md"
        echo
        echo "</details>"
      else
        echo
        echo "_Shadow: the classifier would also have chosen a full render — $(jq -r .reason "$CLASSIFY_DIR/classify.json")._"
      fi
    else
      cat "$CLASSIFY_DIR/classify-summary.md"
    fi
    if [ -f "${DIFF_TEMP}/shadow-check.md" ] && grep -q 'FAILED' "${DIFF_TEMP}/shadow-check.md"; then
      echo
      cat "${DIFF_TEMP}/shadow-check.md"
    fi
  } > "${DIFF_TEMP}/scope.md"
  grep -E '^(shadow_result|shadow_missed)=' "$SHADOW_OUTPUT" >> "$GITHUB_OUTPUT" 2>/dev/null || true
fi

# ── Stage 3: Comment ───────────────────────────────────────────────────────
export BASELINE_STATUS="${INPUT_BASELINE_STATUS:-success}"
export PR_STATUS="${INPUT_PR_STATUS:-success}"
# Read diff status from the diff step's output
export DIFF_STATUS=$(grep '^status=' "$DIFF_OUTPUT" | head -1 | cut -d= -f2-)
export COMMENT_TEMP="$DIFF_TEMP"
export MERGE_FALLBACK="${INPUT_MERGE_FALLBACK:-}"
export BASELINE_FILTER="${INPUT_BASELINE_FILTER:-true}"
export PR_FILTER="${INPUT_PR_FILTER:-true}"
export WORKFLOW_PIN_TARGET="${INPUT_WORKFLOW_PIN_TARGET:-}"
export WORKFLOW_PIN_MERGE="${INPUT_WORKFLOW_PIN_MERGE:-}"
export LATEST_ATLAS_TAG="${INPUT_LATEST_ATLAS_TAG:-}"
export LATEST_MAIN_SHA="${INPUT_LATEST_MAIN_SHA:-}"
export DIFF_TOTAL=$(grep '^total=' "$DIFF_OUTPUT" | head -1 | cut -d= -f2- || echo "0")
export DIFF_RELEASES=$(grep '^releases=' "$DIFF_OUTPUT" | head -1 | cut -d= -f2- || echo "0")
export DIFF_TRUNCATED=$(grep '^truncated=' "$DIFF_OUTPUT" | head -1 | cut -d= -f2- || echo "")
export HELMFILE_PATH="${INPUT_HELMFILE_PATH:-helmfile.yaml.gotmpl}"
export RUN_URL="${INPUT_RUN_URL:-}"
export BASELINE_STDERR_LOG="${INPUT_BASELINE_STDERR_LOG:-${DIFF_TEMP}/baseline-stderr.log}"
export PR_STDERR_LOG="${INPUT_PR_STDERR_LOG:-${DIFF_TEMP}/pr-stderr.log}"

/comment.sh

# ── Forward diff outputs to the action's GITHUB_OUTPUT ─────────────────────
# comment.sh already wrote `body` to $GITHUB_OUTPUT. Forward the diff counters too.
grep -E '^(status|total|releases|suppressed_|truncated)=' "$DIFF_OUTPUT" >> "$GITHUB_OUTPUT" 2>/dev/null || true

# Forward replay counters
grep -E '^(scrubbed_count|no_map_count)=' "$REPLAY_OUTPUT" >> "$GITHUB_OUTPUT" 2>/dev/null || true

# Write the job summary path
if [ -f "${DIFF_TEMP}/summary.md" ]; then
  echo "summary-file=${DIFF_TEMP}/summary.md" >> "$GITHUB_OUTPUT"
fi
