#!/usr/bin/env bats
#
# Scenario: the stage-1 env filters accept comma-separated LISTS.
#
#   ATLAS_FILTER_CLUSTER="a,b"  ATLAS_FILTER_DEPLOYMENT_NAME="x,y"
#
# selects every listed deployment on every listed cluster (cross product).
# Items are trimmed and empty items dropped, so a trailing comma or spaces
# around names are harmless. A variable that holds nothing but separators is
# the same as an unset variable — it must NOT collapse the render to zero
# releases, because a silent empty render is a green review for a PR nobody
# looked at.
#
# The review pipeline's changed-file subsetting depends on this: one helmfile
# invocation per affected cluster carries that cluster's deployment list, so
# an exact set of (cluster, deployment) pairs renders without a per-pair
# state build. filtering.bats pins the single-value contract; this file pins
# the list contract on top of it.

load '../helpers/render'

# pairs_for lists the distinct "cluster deploymentName" pairs helmfile emits
# for the given env-var filters ($1 clusters, $2 deployments; "" = unset).
pairs_for() {
  local clusters="$1" deployments="$2" root
  root="$(_repo_root)"
  ATLAS_FILTER_CLUSTER="$clusters" ATLAS_FILTER_DEPLOYMENT_NAME="$deployments" \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
      --allow-no-matching-release list --output json 2>/dev/null \
    | jq -r '[.[].labels | split(",")
        | map(select(startswith("cluster:") or startswith("deploymentName:")))
        | map(split(":")[1]) | join(" ")] | unique | .[]'
}

setup_file() {
  export UNFILTERED_PAIRS
  UNFILTERED_PAIRS="$(pairs_for "" "")"
  [ -n "$UNFILTERED_PAIRS" ]
}

@test "multi filter: cluster list selects exactly the listed clusters" {
  run pairs_for "cluster1,group1/cluster2" ""
  [ "$status" -eq 0 ]
  # both listed clusters present, nothing else
  grep -q '^cluster1 ' <<< "$output"
  grep -q '^group1/cluster2 ' <<< "$output"
  [ -z "$(grep -vE '^(cluster1|group1/cluster2) ' <<< "$output")" ]
}

@test "multi filter: deployment list selects exactly the listed deployments" {
  run pairs_for "" "deployment1,deployment2"
  [ "$status" -eq 0 ]
  grep -q ' deployment1$' <<< "$output"
  grep -q ' deployment2$' <<< "$output"
  [ -z "$(grep -vE ' (deployment1|deployment2)$' <<< "$output")" ]
}

@test "multi filter: both lists = cross product restricted to existing pairs" {
  # deployment1 exists on cluster1 only, deployment2 on group1/cluster2 only.
  # The cross product asks for four pairs; the two that exist must render,
  # the two that do not exist must not be invented.
  run pairs_for "cluster1,group1/cluster2" "deployment1,deployment2"
  [ "$status" -eq 0 ]
  [ "$(printf '%s\n' "$output" | sort)" = "$(printf 'cluster1 deployment1\ngroup1/cluster2 deployment2\n' | sort)" ]
}

@test "multi filter: whitespace and empty items are ignored" {
  run pairs_for " cluster1 , group1/cluster2 ," " deployment1,, deployment2 "
  [ "$status" -eq 0 ]
  [ "$(printf '%s\n' "$output" | sort)" = "$(printf 'cluster1 deployment1\ngroup1/cluster2 deployment2\n' | sort)" ]
}

@test "multi filter: separators only == unset (no silent empty render)" {
  run pairs_for " , " ","
  [ "$status" -eq 0 ]
  [ "$output" = "$UNFILTERED_PAIRS" ]
}

@test "multi filter: list result equals the union of single-value renders" {
  # The list path must not select more or less than the single-value path
  # would, applied once per item.
  local single_union
  single_union="$( { pairs_for "cluster1" "deployment1"; pairs_for "group1/cluster2" "deployment1"; \
                     pairs_for "cluster1" "deployment2"; pairs_for "group1/cluster2" "deployment2"; } | sort -u)"
  run pairs_for "cluster1,group1/cluster2" "deployment1,deployment2"
  [ "$status" -eq 0 ]
  [ "$(printf '%s\n' "$output" | sort -u)" = "$single_union" ]
}

@test "multi filter: filter lists do not leak into rendered values" {
  # The filter is stripped before the atlas object reaches sub-helmfiles;
  # a rendered ConfigMap (chart1 serializes .Values) must not carry it.
  local root out
  root="$(_repo_root)"
  out="$(ATLAS_FILTER_CLUSTER="cluster1,group1/cluster2" ATLAS_FILTER_DEPLOYMENT_NAME="deployment1,deployment2" \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" template --skip-schema-validation 2>/dev/null)"
  [ -n "$out" ]
  ! grep -qE '^\s+filter:' <<< "$out"
}
