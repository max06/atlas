#!/usr/bin/env bash
# ATLAS snapshot-review: replay captured PR redaction maps over baseline output.
#
# Thin wrapper around scrub-baseline.sh. Handles the case where no maps or no
# baseline exist, and writes the replay status file for the diff step.
#
# Required environment:
#   BASELINE_DIR     — path to baseline render tree
#   SIDEDUMP_MAP_DIR — path to captured redaction maps from the PR render
#   REPLAY_TEMP      — temporary directory for status file (defaults to $RUNNER_TEMP)
#
# Optional environment:
#   SCRUB_SCRIPT     — path to scrub-baseline.sh (defaults to ATLAS_PLUGIN_DIR)
#   ATLAS_PLUGIN_DIR — path to the atlas-redact plugin directory
#   GITHUB_OUTPUT    — output file for step outputs (default: /dev/null)
#
# Outputs (written to $GITHUB_OUTPUT):
#   scrubbed_count  — number of releases successfully scrubbed
#   no_map_count    — number of releases with no matching map

set -euo pipefail

BASELINE_DIR="${BASELINE_DIR:-}"
SIDEDUMP_MAP_DIR="${SIDEDUMP_MAP_DIR:-}"
REPLAY_TEMP="${REPLAY_TEMP:-${RUNNER_TEMP:-/tmp}}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"
SCRUB_SCRIPT="${SCRUB_SCRIPT:-${ATLAS_PLUGIN_DIR:-}/scrub-baseline.sh}"

STATUS_FILE="${REPLAY_TEMP}/replay-status.txt"
: > "$STATUS_FILE"

if [ ! -d "$BASELINE_DIR" ]; then
  echo "No baseline output to replay against — skipping."
  echo "scrubbed_count=0" >> "$GITHUB_OUTPUT"
  echo "no_map_count=0" >> "$GITHUB_OUTPUT"
  exit 0
fi

if [ ! -d "$SIDEDUMP_MAP_DIR" ]; then
  echo "::warning::No PR-side redaction maps captured — baseline diffs will be suppressed to prevent leaks from older ATLAS versions."
  mkdir -p "$SIDEDUMP_MAP_DIR"
fi

if [ ! -x "$SCRUB_SCRIPT" ]; then
  echo "::warning::scrub-baseline.sh not found at $SCRUB_SCRIPT — skipping replay."
  echo "scrubbed_count=0" >> "$GITHUB_OUTPUT"
  echo "no_map_count=0" >> "$GITHUB_OUTPUT"
  exit 0
fi

"$SCRUB_SCRIPT" "$BASELINE_DIR" "$SIDEDUMP_MAP_DIR" | tee "$STATUS_FILE"

SCRUBBED=$(grep -c '^scrubbed ' "$STATUS_FILE" || true)
NO_MAP=$(grep -c '^no-map ' "$STATUS_FILE" || true)
echo "scrubbed_count=$SCRUBBED" >> "$GITHUB_OUTPUT"
echo "no_map_count=$NO_MAP" >> "$GITHUB_OUTPUT"
