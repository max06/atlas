#!/usr/bin/env bash
# ATLAS snapshot-review: render one side (PR or baseline) of a helmfile deployment.
#
# Parameterized to handle both merge-result and target-branch renders. The caller
# sets environment variables to control behavior; this script writes results to
# $GITHUB_OUTPUT (or a caller-specified output file for testing).
#
# Required environment:
#   HELMFILE_PATH        — path to the helmfile entry point
#   SNAPSHOT_LABEL       — "pr" or "baseline" (names the output directory)
#   RENDER_TEMP          — temporary directory for output (defaults to $RUNNER_TEMP)
#
# Optional environment:
#   ATLAS_SIDEDUMP_MAP_DIR — if set, enables redaction-map side-dump (PR render only)
#   MERGE_FALLBACK         — "true" if merge ref was unavailable (PR render only)
#   SOPS_AGE_KEY           — SOPS age private key (written to a temp file)
#   SOPS_AGE_KEY_FILE      — pre-existing SOPS key file path (takes precedence)
#   GITHUB_OUTPUT          — output file for step outputs (default: /dev/null)
#   BLOCKING               — "true" if render errors should be reported as errors
#                            (PR side); "false" for warnings (baseline side).
#                            Default: "true"
#
# Outputs (written to $GITHUB_OUTPUT):
#   status            — success / error / missing
#   snapshot_dir      — path to rendered output tree
#   sidedump_dir      — path to captured redaction maps (only when sidedump enabled)
#   list_json         — path to helmfile list JSON output
#   filter_supported  — true/false (whether --selector filtering worked)
#   merge_fallback    — true (only when MERGE_FALLBACK was set)
#   workflow_pin      — detected ATLAS workflow ref from caller's workflow file

set -euo pipefail

HELMFILE_PATH="${HELMFILE_PATH:?HELMFILE_PATH is required}"
SNAPSHOT_LABEL="${SNAPSHOT_LABEL:?SNAPSHOT_LABEL is required}"
RENDER_TEMP="${RENDER_TEMP:-${RUNNER_TEMP:-/tmp}}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"
BLOCKING="${BLOCKING:-true}"

HELMFILE_ARGS="-f $HELMFILE_PATH"
STDERR_LOG="${RENDER_TEMP}/${SNAPSHOT_LABEL}-stderr.log"
SNAPSHOT_DIR="${RENDER_TEMP}/${SNAPSHOT_LABEL}-snapshot"
LIST_JSON="${RENDER_TEMP}/${SNAPSHOT_LABEL}-list.json"
LEVEL_TAG=$( [ "$BLOCKING" = "true" ] && echo "error" || echo "warning" )
SIDE_LABEL=$( [ "$SNAPSHOT_LABEL" = "pr" ] && echo "merge result" || echo "target branch" )

# ── SOPS key setup ──────────────────────────────────────────────────────────
if [ -z "${SOPS_AGE_KEY_FILE:-}" ] && [ -n "${SOPS_AGE_KEY:-}" ]; then
  KEY_FILE="${RENDER_TEMP}/sops-age-key.txt"
  printf '%s' "$SOPS_AGE_KEY" > "$KEY_FILE"
  export SOPS_AGE_KEY_FILE="$KEY_FILE"
  echo "SOPS key file: $(wc -c < "$KEY_FILE") bytes"
fi

# ── Helmfile not found (baseline may not have it yet) ───────────────────────
if [ ! -f "$HELMFILE_PATH" ]; then
  echo "Helmfile not found at $HELMFILE_PATH"
  echo "status=missing" >> "$GITHUB_OUTPUT"
  exit 0
fi

# ── Discover deployments ────────────────────────────────────────────────────
if ! DEPLOY_JSON=$(ATLAS_REDACT_SECRETS=true helmfile $HELMFILE_ARGS list \
  --output json 2>"$STDERR_LOG"); then
  echo "::${LEVEL_TAG}::Failed to list deployments on ${SIDE_LABEL}"
  echo "status=error" >> "$GITHUB_OUTPUT"
  cat "$STDERR_LOG"
  exit 0
fi
echo "$DEPLOY_JSON" > "$LIST_JSON"
echo "list_json=$LIST_JSON" >> "$GITHUB_OUTPUT"

TOTAL=$(jq length "$LIST_JSON")

# ── Selector-support probe ──────────────────────────────────────────────────
# Tests whether CLI --selector reaches release-level commonLabels. On current
# ATLAS this always passes; consumers pinned to older ATLAS refs with hardcoded
# sub-helmfile selectors will fail the probe and fall back to bulk rendering.
PROBE_OUT=$(ATLAS_REDACT_SECRETS=true \
  helmfile $HELMFILE_ARGS \
  list --selector cluster=__atlas_probe_nonexistent__ \
  --output json --allow-no-matching-release 2>/dev/null || true)
if [ -z "$PROBE_OUT" ]; then
  PROBE_COUNT=0
else
  PROBE_COUNT=$(echo "$PROBE_OUT" | jq 'length' 2>/dev/null || echo "$TOTAL")
fi
if [ "$PROBE_COUNT" = "0" ]; then
  FILTER_SUPPORTED=true
else
  FILTER_SUPPORTED=false
  echo "::warning::helmfile --selector did not narrow releases on the ${SIDE_LABEL} side — falling back to bulk render."
  echo "::group::Probe diagnostic (${SNAPSHOT_LABEL})"
  echo "PROBE_COUNT=$PROBE_COUNT (expected 0 if selector works)"
  echo "PROBE_OUT (first 400 chars):"
  echo "${PROBE_OUT:0:400}"
  echo "::endgroup::"
fi
echo "filter_supported=$FILTER_SUPPORTED" >> "$GITHUB_OUTPUT"

# ── Render ──────────────────────────────────────────────────────────────────
OUTPUT_DIR_TEMPLATE='{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}'

RENDER_STATUS=0
if [ "$FILTER_SUPPORTED" = "true" ]; then
  # Fast path: one helmfile template per (cluster, deploymentName) pair.
  PAIRS=$(jq -r '[.[] | (.labels | split(",") |
    map(select(startswith("cluster:") or startswith("deploymentName:")) |
      split(":") | {(.[0]): .[1]}) | add)]
    | unique_by(.cluster + "|" + .deploymentName)
    | .[] | "\(.cluster)|\(.deploymentName)"' "$LIST_JSON")
  export ATLAS_REDACT_SECRETS=true
  export HELMFILE_PATH
  export RENDER_DIR="$SNAPSHOT_DIR"
  export STDERR_LOG
  # ATLAS_SIDEDUMP_MAP_DIR is inherited from caller if set
  render_one() {
    local pair="$1" cluster deployment
    cluster="${pair%|*}"
    deployment="${pair#*|}"
    ATLAS_FILTER_CLUSTER="$cluster" \
    ATLAS_FILTER_DEPLOYMENT_NAME="$deployment" \
    helmfile -f "$HELMFILE_PATH" \
      template \
      --selector "cluster=$cluster,deploymentName=$deployment" \
      --skip-schema-validation \
      --output-dir "$RENDER_DIR" \
      --output-dir-template "$OUTPUT_DIR_TEMPLATE" \
      2>>"$STDERR_LOG" \
    || { echo "::${LEVEL_TAG}::Render failed for $cluster/$deployment on ${SIDE_LABEL}" >&2; return 1; }
  }
  export -f render_one
  export LEVEL_TAG SIDE_LABEL OUTPUT_DIR_TEMPLATE
  printf '%s\n' "$PAIRS" | xargs -r -P4 -I{} bash -c 'render_one "{}"' || RENDER_STATUS=$?
else
  # Bulk path: single helmfile template invocation.
  ATLAS_REDACT_SECRETS=true helmfile $HELMFILE_ARGS template \
    --skip-schema-validation \
    --output-dir "$SNAPSHOT_DIR" \
    --output-dir-template "$OUTPUT_DIR_TEMPLATE" \
    2>>"$STDERR_LOG" || RENDER_STATUS=$?
fi

if [ $RENDER_STATUS -ne 0 ]; then
  echo "::${LEVEL_TAG}::${SIDE_LABEL^} render failed"
  echo "status=error" >> "$GITHUB_OUTPUT"
else
  echo "status=success" >> "$GITHUB_OUTPUT"
fi
echo "snapshot_dir=$SNAPSHOT_DIR" >> "$GITHUB_OUTPUT"

# ── Sidedump directory ──────────────────────────────────────────────────────
# Backfill empty map files for releases that had no secrets. The redaction
# pipeline only writes a sidedump when the replacement map is non-empty, so
# secret-free releases (e.g. rook-ceph) get no file. The replay step treats
# a missing file as "old ATLAS version — suppress diff", which causes ghost
# entries. An empty JSON object signals "no secrets, safe to diff".
if [ -n "${ATLAS_SIDEDUMP_MAP_DIR:-}" ] && [ -d "$SNAPSHOT_DIR" ]; then
  while IFS= read -r templates_dir; do
    [ -z "$templates_dir" ] && continue
    release_dir="$(dirname "$(dirname "$templates_dir")")"
    release_path="${release_dir#"$SNAPSHOT_DIR"/}"
    map_file="${ATLAS_SIDEDUMP_MAP_DIR}/${release_path}.json"
    if [ ! -f "$map_file" ]; then
      mkdir -p "$(dirname "$map_file")"
      echo '{}' > "$map_file"
    fi
  done < <(find "$SNAPSHOT_DIR" -type d -name templates 2>/dev/null | sort -u)
  echo "sidedump_dir=$ATLAS_SIDEDUMP_MAP_DIR" >> "$GITHUB_OUTPUT"
fi

# ── Merge fallback flag ────────────────────────────────────────────────────
if [ "${MERGE_FALLBACK:-}" = "true" ]; then
  echo "merge_fallback=true" >> "$GITHUB_OUTPUT"
fi

# ── Detect workflow pin ─────────────────────────────────────────────────────
# Parses the caller's workflow file for the ATLAS review workflow ref.
REF=$(grep -hE 'uses:[[:space:]]*max06/atlas/\.github/workflows/snapshot-review\.yml@' \
  .github/workflows/*.y*ml 2>/dev/null | \
  sed -E 's/.*snapshot-review\.yml@([^[:space:]]+).*/\1/' | \
  head -1) || true
echo "workflow_pin=${REF:-}" >> "$GITHUB_OUTPUT"
echo "${SIDE_LABEL^} ATLAS pin: ${REF:-<not detected>}"

# ── Debug output ────────────────────────────────────────────────────────────
if [ -s "$STDERR_LOG" ]; then
  echo "::group::${SIDE_LABEL^} render output"
  cat "$STDERR_LOG"
  echo "::endgroup::"
fi
