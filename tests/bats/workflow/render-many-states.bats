#!/usr/bin/env bats
# Regression test for .github/actions/atlas-render/render.sh discovery.
#
# `helmfile list` (helmfile >= 1.2.0) deadlocks when more than 100 states carry
# releases: ListReleases buffers per-state results in a 100-slot channel that is
# drained only after every state was visited, so the 101st state blocks forever
# with no output. render.sh used `helmfile list` for discovery and hung in CI
# until the job timeout once a consumer repo crossed that line.
#
# This test builds a plain helmfile tree with 101 nested states (no ATLAS
# pipeline involved — the fixture is about state count, not value loading) and
# requires render.sh to discover and render all of them within a hard timeout.

load '../helpers/render'

STATE_COUNT=101

_render_script() {
  echo "$(_repo_root)/.github/actions/atlas-render/render.sh"
}

# Build the fixture once per file: one local dummy chart plus STATE_COUNT
# states, each with its own cluster/deploymentName labels and matching
# environment values (render.sh's --output-dir-template reads
# .Environment.Values.atlas.deployment.*).
setup_file() {
  export MANY_TEMP="${BATS_FILE_TMPDIR}/many-states"
  mkdir -p "$MANY_TEMP/states" "$MANY_TEMP/chart/templates"

  printf 'apiVersion: v2\nname: dummy\nversion: 0.1.0\n' > "$MANY_TEMP/chart/Chart.yaml"
  printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\n' \
    > "$MANY_TEMP/chart/templates/configmap.yaml"

  {
    echo 'helmfiles:'
    for i in $(seq 1 "$STATE_COUNT"); do
      cat > "$MANY_TEMP/states/state-$i.yaml" <<EOF
environments:
  default:
    values:
      - atlas:
          deployment:
            cluster: cluster-$i
            deploymentName: deployment-$i
---
commonLabels:
  cluster: cluster-$i
  deploymentName: deployment-$i
releases:
  - name: release-$i
    chart: ../chart
EOF
      echo "  - path: states/state-$i.yaml"
    done
  } > "$MANY_TEMP/helmfile.yaml"

  export GITHUB_OUTPUT="${MANY_TEMP}/github-output"
  : > "$GITHUB_OUTPUT"
  export HELMFILE_PATH="$MANY_TEMP/helmfile.yaml"
  export SNAPSHOT_LABEL="many"
  export RENDER_TEMP="$MANY_TEMP"
  export BLOCKING="true"

  # A hang is the failure mode under test, so cap the run hard. The deadlock
  # ignores SIGTERM, hence -s KILL. 124/137 = timeout fired.
  timeout -s KILL 600 bash "$(_render_script)" \
    > "${MANY_TEMP}/render.log" 2>&1 \
    && echo 0 > "${MANY_TEMP}/exit-code" \
    || echo $? > "${MANY_TEMP}/exit-code"
}

get_output() {
  grep "^${1}=" "$GITHUB_OUTPUT" | head -1 | cut -d= -f2-
}

@test "render.sh: discovery does not hang on more than 100 states" {
  local code
  code="$(cat "${MANY_TEMP}/exit-code")"
  if [ "$code" != "0" ]; then
    echo "render.sh exit code: $code (124/137 = timeout, i.e. the discovery hung)"
    cat "${MANY_TEMP}/render.log"
  fi
  [ "$code" = "0" ]
  [ "$(get_output status)" = "success" ]
}

@test "render.sh: discovery JSON lists every state's release" {
  local list_json
  list_json="$(get_output list_json)"
  [ -f "$list_json" ]
  [ "$(jq length "$list_json")" -eq "$STATE_COUNT" ]
  # list-compatible shape: name, namespace, and "k:v,k:v" labels
  [ "$(jq -r '.[0] | keys | join(",")' "$list_json")" = "labels,name,namespace" ]
  jq -r '.[].labels' "$list_json" | grep -q "^cluster:cluster-${STATE_COUNT},deploymentName:deployment-${STATE_COUNT}$"
}

@test "render.sh: every state was rendered into its own snapshot directory" {
  local snapshot_dir
  snapshot_dir="$(get_output snapshot_dir)"
  [ -d "$snapshot_dir/cluster-1/deployment-1/release-1" ]
  [ -d "$snapshot_dir/cluster-${STATE_COUNT}/deployment-${STATE_COUNT}/release-${STATE_COUNT}" ]
  [ "$(find "$snapshot_dir" -name configmap.yaml | wc -l)" -eq "$STATE_COUNT" ]
}
