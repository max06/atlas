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
#   RENDER_PAIRS_FILE      — subset mode: render ONLY the "<cluster>|<deployment>"
#                            pairs listed in this file (one per line, produced by
#                            classify.sh for this side). Discovery and the selector
#                            probe are skipped — the pairs came from this side's own
#                            discovery map, so they are known to exist. An empty
#                            file renders nothing and reports success (this side has
#                            no selected pair — e.g. every selected deployment is new).
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
#   render_mode       — full / subset
#   rendered_pairs    — number of (cluster, deployment) pairs this run rendered
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

# ── Subset mode ─────────────────────────────────────────────────────────────
# The pairs file replaces discovery: the review classifier derived it from this
# side's discovery map (ATLAS_DISCOVERY_MAP, see discover.sh + classify.sh).
# One invocation per cluster carries that cluster's deployment list through the
# multi-value stage-1 filter, so an exact pair set renders without per-pair
# state builds. Both --selector and ATLAS_FILTER_* are passed, as in the full
# path: the env filter avoids parsing unrelated states, the selector is the
# release-level safety net.
if [ -n "${RENDER_PAIRS_FILE:-}" ]; then
  [ -f "$RENDER_PAIRS_FILE" ] || { echo "::error::RENDER_PAIRS_FILE not found: $RENDER_PAIRS_FILE"; echo "status=error" >> "$GITHUB_OUTPUT"; exit 0; }
  mkdir -p "$SNAPSHOT_DIR"
  : > "$STDERR_LOG"
  PAIR_COUNT=$(grep -c '|' "$RENDER_PAIRS_FILE" || true)
  echo "Subset render: ${PAIR_COUNT} pair(s) from $RENDER_PAIRS_FILE"
  RENDER_STATUS=0
  if [ "$PAIR_COUNT" -gt 0 ]; then
    export ATLAS_REDACT_SECRETS=true
    export HELMFILE_PATH STDERR_LOG LEVEL_TAG SIDE_LABEL
    export RENDER_DIR="$SNAPSHOT_DIR"
    export OUTPUT_DIR_TEMPLATE='{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}'
    render_cluster() {
      # $1 = "<cluster>|<d1>,<d2>,..."
      local cluster="${1%%|*}" deployments="${1#*|}" selectors=()
      local d
      IFS=',' read -r -a ds <<< "$deployments"
      for d in "${ds[@]}"; do selectors+=(--selector "cluster=$cluster,deploymentName=$d"); done
      ATLAS_FILTER_CLUSTER="$cluster" \
      ATLAS_FILTER_DEPLOYMENT_NAME="$deployments" \
      helmfile -f "$HELMFILE_PATH" \
        template "${selectors[@]}" \
        --skip-schema-validation \
        --output-dir "$RENDER_DIR" \
        --output-dir-template "$OUTPUT_DIR_TEMPLATE" \
        2>>"$STDERR_LOG" \
      || { echo "::${LEVEL_TAG}::Render failed for $cluster [$deployments] on ${SIDE_LABEL}" >&2; return 1; }
    }
    export -f render_cluster
    # group "<cluster>|<deployment>" lines into one "<cluster>|<d1>,<d2>" per cluster
    sort -u "$RENDER_PAIRS_FILE" | grep '|' \
      | awk -F'|' '{ if ($1 in acc) acc[$1]=acc[$1] "," $2; else acc[$1]=$2 } END { for (c in acc) print c "|" acc[c] }' \
      | xargs -r -P4 -I{} bash -c 'render_cluster "{}"' || RENDER_STATUS=$?
  fi
  if [ $RENDER_STATUS -ne 0 ]; then
    echo "::${LEVEL_TAG}::${SIDE_LABEL^} subset render failed"
    echo "status=error" >> "$GITHUB_OUTPUT"
  else
    echo "status=success" >> "$GITHUB_OUTPUT"
  fi
  {
    echo "snapshot_dir=$SNAPSHOT_DIR"
    echo "filter_supported=true"
    echo "render_mode=subset"
    echo "rendered_pairs=$PAIR_COUNT"
  } >> "$GITHUB_OUTPUT"
  SKIP_FULL_RENDER=true
fi

if [ "${SKIP_FULL_RENDER:-}" != "true" ]; then
# ── Discover deployments ────────────────────────────────────────────────────
# Discovery deliberately avoids `helmfile list`. Since helmfile v1.2.0 its
# ListReleases collects per-state results through a channel with a fixed buffer
# of 100 that is drained only after every state was visited, so a tree with more
# than 100 states carrying releases deadlocks: no output, no error, and the
# process ignores SIGTERM (the CI job then runs into its timeout).
# `helmfile build` streams one YAML document per state instead. Each document
# carries the state's commonLabels and releases, which is all the pair derivation
# below needs. The documents are converted into the JSON shape that
# `helmfile list --output json` produces (name, namespace, labels as "k:v,k:v")
# so downstream consumers keep working. The build output itself is never stored:
# it embeds rendered values, which may contain decrypted secrets. yq only turns
# the YAML document stream into one JSON array; the shaping happens in jq, whose
# variable binding (`as $state`) is lexically scoped — yq's would produce a
# cross product over all states.
DISCOVERY_QUERY='map(
  select(.commonLabels != null) | . as $state
  | (.releases // [])[]
  | {
      name: .name,
      namespace: (.namespace // ""),
      labels: ($state.commonLabels | to_entries | sort_by(.key)
               | map("\(.key):\(.value)") | join(","))
    })'
if ! helmfile $HELMFILE_ARGS build --embed-values=false 2>"$STDERR_LOG" \
  | yq eval-all -o=json '[.]' - \
  | jq "$DISCOVERY_QUERY" > "$LIST_JSON"; then
  echo "::${LEVEL_TAG}::Failed to discover deployments on ${SIDE_LABEL}"
  echo "status=error" >> "$GITHUB_OUTPUT"
  cat "$STDERR_LOG"
  exit 0
fi
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
echo "render_mode=full" >> "$GITHUB_OUTPUT"
echo "rendered_pairs=$(printf '%s\n' "${PAIRS:-}" | grep -c '|' || true)" >> "$GITHUB_OUTPUT"
fi  # SKIP_FULL_RENDER

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
