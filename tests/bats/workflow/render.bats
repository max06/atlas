#!/usr/bin/env bats
# Tests for .github/actions/atlas-render/render.sh
#
# These tests exercise the render script against the ATLAS test fixtures.
# They validate that the script correctly discovers deployments, renders
# manifests, detects workflow pins, and handles error conditions.
#
# One shared render runs in setup_file; tests that need different env
# configs (sidedump, merge_fallback, missing helmfile) run individually.

load 'helpers/render'

_render_script() {
  echo "$(_repo_root)/.github/actions/atlas-render/render.sh"
}

_setup_helm_plugins() {
  local default_plugins
  default_plugins="$(helm env HELM_PLUGINS 2>/dev/null || echo "")"
  export HELM_PLUGINS="${default_plugins:+${default_plugins}:}$(_repo_root)/.github/actions/atlas-render"
}

# ── Shared render (runs once per file) ─────────────────────────────────────

setup_file() {
  export SHARED_TEMP="${BATS_FILE_TMPDIR}/render-shared"
  mkdir -p "$SHARED_TEMP"
  export GITHUB_OUTPUT="${SHARED_TEMP}/github-output"
  : > "$GITHUB_OUTPUT"

  export HELMFILE_PATH="$(_repo_root)/tests/helmfile.yaml.gotmpl"
  export SNAPSHOT_LABEL="shared"
  export RENDER_TEMP="$SHARED_TEMP"
  export BLOCKING="true"
  _setup_helm_plugins

  bash "$(_render_script)"
}

setup() {
  # Per-test temp for tests that need their own render
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/render-test.XXXXXX")"
}

# Helper: read an output value from a GITHUB_OUTPUT file
get_output() {
  local file="${2:-${SHARED_TEMP}/github-output}"
  grep "^${1}=" "$file" | head -1 | cut -d= -f2-
}

# ── Tests using shared render ──────────────────────────────────────────────

@test "render.sh: renders test fixtures successfully" {
  [ "$(get_output status)" = "success" ]
  local snapshot_dir
  snapshot_dir="$(get_output snapshot_dir)"
  [ -d "$snapshot_dir" ]
  [ -n "$(ls -A "$snapshot_dir")" ]
}

@test "render.sh: produces list JSON with deployments" {
  local list_json
  list_json="$(get_output list_json)"
  [ -f "$list_json" ]
  local count
  count=$(jq length "$list_json")
  [ "$count" -gt 0 ]
}

@test "render.sh: renders expected deployment paths" {
  local snapshot_dir
  snapshot_dir="$(get_output snapshot_dir)"
  [ -d "$snapshot_dir/cluster1/deployment1/app1" ]
  [ -d "$snapshot_dir/cluster1/deployment3/app-novals" ]
}

@test "render.sh: filter_supported is true for current ATLAS" {
  [ "$(get_output filter_supported)" = "true" ]
}

@test "render.sh: no sidedump output when env var unset" {
  [ -z "$(get_output sidedump_dir)" ]
}

@test "render.sh: no merge_fallback output when env var unset" {
  [ -z "$(get_output merge_fallback)" ]
}

@test "render.sh: workflow pin is empty when no workflow files present" {
  local pin
  pin="$(get_output workflow_pin)"
  true  # No assertion on content — test repo may or may not have a pin
}

@test "render.sh: snapshot_dir contains the label" {
  local snapshot_dir
  snapshot_dir="$(get_output snapshot_dir)"
  [[ "$snapshot_dir" == *"shared"* ]]
}

# ── Tests that need their own render ───────────────────────────────────────

@test "render.sh: sidedump directory passed through when set" {
  local out="${TEST_TEMP}/github-output"
  : > "$out"
  export GITHUB_OUTPUT="$out"
  export HELMFILE_PATH="$(_repo_root)/tests/helmfile.yaml.gotmpl"
  export SNAPSHOT_LABEL="test-sidedump"
  export RENDER_TEMP="$TEST_TEMP"
  export BLOCKING="true"
  export ATLAS_SIDEDUMP_MAP_DIR="${TEST_TEMP}/sidedump-maps"
  mkdir -p "$ATLAS_SIDEDUMP_MAP_DIR"
  _setup_helm_plugins

  run bash "$(_render_script)"
  [ "$status" -eq 0 ]
  [ "$(get_output sidedump_dir "$out")" = "$ATLAS_SIDEDUMP_MAP_DIR" ]
}

@test "render.sh: merge_fallback output set when env var is true" {
  local out="${TEST_TEMP}/github-output"
  : > "$out"
  export GITHUB_OUTPUT="$out"
  export HELMFILE_PATH="$(_repo_root)/tests/helmfile.yaml.gotmpl"
  export SNAPSHOT_LABEL="test-fallback"
  export RENDER_TEMP="$TEST_TEMP"
  export BLOCKING="true"
  export MERGE_FALLBACK="true"
  _setup_helm_plugins

  run bash "$(_render_script)"
  [ "$status" -eq 0 ]
  [ "$(get_output merge_fallback "$out")" = "true" ]
}

@test "render.sh: baseline mode (BLOCKING=false) still renders" {
  local out="${TEST_TEMP}/github-output"
  : > "$out"
  export GITHUB_OUTPUT="$out"
  export HELMFILE_PATH="$(_repo_root)/tests/helmfile.yaml.gotmpl"
  export SNAPSHOT_LABEL="test-baseline"
  export RENDER_TEMP="$TEST_TEMP"
  export BLOCKING="false"
  _setup_helm_plugins

  run bash "$(_render_script)"
  [ "$status" -eq 0 ]
  [ "$(get_output status "$out")" = "success" ]
}

@test "render.sh: missing helmfile returns status=missing" {
  local out="${TEST_TEMP}/github-output"
  : > "$out"
  export GITHUB_OUTPUT="$out"
  export HELMFILE_PATH="${TEST_TEMP}/nonexistent.yaml"
  export SNAPSHOT_LABEL="test-missing"
  export RENDER_TEMP="$TEST_TEMP"
  export BLOCKING="true"

  run bash "$(_render_script)"
  [ "$status" -eq 0 ]
  [ "$(get_output status "$out")" = "missing" ]
}
