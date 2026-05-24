#!/usr/bin/env bats
# Tests for duplicate deployment detection.
# When the same deployment name exists at multiple hierarchy levels,
# the most specific definition wins (cluster > group > global).

load 'helpers/render'

setup_file() {
  ensure_rendered
}

@test "dedup: cluster2 renders deployment50 only once" {
  local count
  count=$(find "$RENDER_DIR" -path "*/group1/cluster2/deployment50/*" -name "*.yaml" | wc -l)
  [ "$count" -gt 0 ]
}

@test "dedup: cluster2 deployment50 uses cluster-level values" {
  values=$(values_for "group1/cluster2" "deployment50" "app-dedup")
  source=$(echo "$values" | yq '.source')
  [ "$source" = "cluster" ]
}

@test "dedup: cluster2 deployment50 has cluster-level deploymentPath" {
  values=$(values_for "group1/cluster2" "deployment50" "app-dedup")
  path=$(echo "$values" | yq '.atlas.deployment.deploymentPath')
  [[ "$path" == *"/group1/cluster2/apps/deployment50/deployment.yaml" ]]
}

@test "dedup: cluster3 deployment50 uses group-level values (no override)" {
  values=$(values_for "group1/cluster3" "deployment50" "app-dedup")
  source=$(echo "$values" | yq '.source')
  [ "$source" = "group" ]
}

@test "dedup: cluster3 deployment50 has group-level deploymentPath" {
  values=$(values_for "group1/cluster3" "deployment50" "app-dedup")
  path=$(echo "$values" | yq '.atlas.deployment.deploymentPath')
  [[ "$path" == *"/group1/apps/deployment50/deployment.yaml" ]]
}
