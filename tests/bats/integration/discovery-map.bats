#!/usr/bin/env bats
#
# Scenario: ATLAS_DISCOVERY_MAP=1 makes `helmfile build` print the discovery
# result — every (cluster, deployment) pair, its deployment.yaml path and the
# app templates it instantiates — instead of the per-pair sub-helmfiles.
#
# The review pipeline's changed-file subsetting consumes this map for both
# revisions of a PR. Its correctness contract is therefore: the map must list
# EXACTLY the pairs the real render would produce (same discovery code, same
# deployment.yaml parse), and it must be cheap (no release states, no values
# loader, no SOPS). A map that misses a pair would let a change to that pair
# through review unrendered.

load '../helpers/render'

# discovery_map prints the atlasDiscovery object as JSON for the test entry
# point. Extra helmfile args (e.g. --state-values-set) are passed through.
discovery_map() {
  local root
  root="$(_repo_root)"
  ATLAS_DISCOVERY_MAP=1 helmfile -f "$root/tests/helmfile.yaml.gotmpl" "$@" \
    build --allow-no-matching-release 2>/dev/null \
    | yq eval-all -o=json '[.]' - \
    | jq '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0]'
}

# real_pairs prints "cluster deploymentName" per line from the real state
# build — the same derivation render.sh uses for discovery.
real_pairs() {
  local root
  root="$(_repo_root)"
  helmfile -f "$root/tests/helmfile.yaml.gotmpl" build --embed-values=false 2>/dev/null \
    | yq eval-all -o=json '[.]' - \
    | jq -r 'map(select(.commonLabels != null) | .commonLabels | .cluster + " " + .deploymentName) | unique | .[]'
}

setup_file() {
  export MAP_JSON="${BATS_FILE_TMPDIR}/map.json"
  discovery_map > "$MAP_JSON"
  [ -s "$MAP_JSON" ]
  ensure_rendered
}

@test "discovery map: header fields (version, roots as configured)" {
  [ "$(jq -r .version "$MAP_JSON")" = "1" ]
  [ "$(jq -r .deploymentsRoot "$MAP_JSON")" = "deployments" ]
  [ "$(jq -r .templatesRoot "$MAP_JSON")" = "templates" ]
}

@test "discovery map: pair set is identical to the real discovery" {
  local from_map from_real
  from_map="$(jq -r '.pairs[] | .cluster + " " + .deploymentName' "$MAP_JSON" | sort)"
  from_real="$(real_pairs | sort)"
  [ -n "$from_real" ]
  [ "$from_map" = "$from_real" ]
}

@test "discovery map: templates per deployment are the deployment.yaml apps[].template, unique and sorted" {
  # deployment7 instantiates two different templates
  [ "$(jq -c '.pairs[] | select(.cluster=="cluster1" and .deploymentName=="deployment7") | .templates' "$MAP_JSON")" = '["app-novals-b","app1"]' ]
  # deployment9 instantiates the same template twice (named instances) → one edge
  [ "$(jq -c '.pairs[] | select(.cluster=="cluster1" and .deploymentName=="deployment9") | .templates' "$MAP_JSON")" = '["app-named"]' ]
}

@test "discovery map: every pair has at least one non-empty template" {
  [ "$(jq '[.pairs[] | select((.templates | length) == 0 or (.templates | index("")))] | length' "$MAP_JSON")" = "0" ]
}

@test "discovery map: deploymentPath is repo-relative and points at an existing file" {
  local root path
  root="$(_repo_root)"
  path="$(jq -r '.pairs[] | select(.cluster=="group1/cluster2" and .deploymentName=="deployment2") | .deploymentPath' "$MAP_JSON")"
  [ "$path" = "deployments/group1/cluster2/apps/deployment2/deployment.yaml" ]
  [ -f "$root/tests/$path" ]
  # no absolute paths anywhere
  [ "$(jq '[.pairs[].deploymentPath | select(startswith("/"))] | length' "$MAP_JSON")" = "0" ]
}

@test "discovery map: group-level deployment is listed once per cluster of the group, pointing at the group file" {
  # deployment6 lives in group1/apps → present on cluster2 and cluster3, same path
  local paths
  paths="$(jq -r '.pairs[] | select(.deploymentName=="deployment6") | .cluster + " " + .deploymentPath' "$MAP_JSON" | sort)"
  [ "$paths" = "$(printf 'group1/cluster2 deployments/group1/apps/deployment6/deployment.yaml\ngroup1/cluster3 deployments/group1/apps/deployment6/deployment.yaml')" ]
}

@test "discovery map: clusterGroup only for grouped clusters, clusterName is the leaf" {
  [ "$(jq -r '.pairs[] | select(.cluster=="group1/cluster2") | .clusterGroup' "$MAP_JSON" | sort -u)" = "group1" ]
  [ "$(jq -r '.pairs[] | select(.cluster=="group1/cluster2") | .clusterName' "$MAP_JSON" | sort -u)" = "cluster2" ]
  [ "$(jq '[.pairs[] | select(.cluster=="cluster1") | has("clusterGroup")] | any' "$MAP_JSON")" = "false" ]
}

@test "discovery map: no release states are emitted (cheap by construction)" {
  local root releases
  root="$(_repo_root)"
  releases="$(ATLAS_DISCOVERY_MAP=1 helmfile -f "$root/tests/helmfile.yaml.gotmpl" build --allow-no-matching-release 2>/dev/null \
    | yq eval-all -o=json '[.]' - | jq '[.[] | (.releases // []) | length] | add')"
  [ "$releases" = "0" ]
}

@test "discovery map: stage-1 filters narrow the map" {
  # bats runs each test in its own subshell, so these exports stay local.
  export ATLAS_FILTER_CLUSTER=group1/cluster2
  export ATLAS_FILTER_DEPLOYMENT_NAME=deployment2,deployment6
  local pairs
  pairs="$(discovery_map | jq -c '[.pairs[] | .cluster + "/" + .deploymentName] | sort')"
  [ "$pairs" = '["group1/cluster2/deployment2","group1/cluster2/deployment6"]' ]
}

@test "discovery map: repo with global deployments but no clusters yields an empty pair list" {
  run discovery_map --state-values-set atlas.deploymentDefinitions=fixtures-global-only
  [ "$status" -eq 0 ]
  [ "$(jq -c '.pairs' <<< "$output")" = "[]" ]
}

@test "discovery map: duplicate leaf cluster names fail the map like they fail the render" {
  local root
  root="$(_repo_root)"
  run bash -c "ATLAS_DISCOVERY_MAP=1 helmfile -f '$root/tests/helmfile.yaml.gotmpl' --state-values-set atlas.deploymentDefinitions=fixtures-dup-leaf build --allow-no-matching-release"
  [ "$status" -ne 0 ]
  [[ "$output" == *"duplicate leaf cluster name"* ]]
}

@test "discovery map: the mode flag does not leak into rendered values" {
  # Normal render: chart1 serializes .Values; neither the flag nor a map may
  # appear there.
  ! grep -rq 'discoveryMap' "$RENDER_DIR"
  ! grep -rq 'atlasDiscovery' "$RENDER_DIR"
}
