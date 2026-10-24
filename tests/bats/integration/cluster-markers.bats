#!/usr/bin/env bats
#
# Scenario: cluster detection by marker file and nested groups.
#
#   marker rule  a directory with cluster.yaml is a cluster; it needs no
#                apps/ directory of its own
#   legacy rule  a directory with apps/ and no other cluster candidate below
#                it is a cluster (else a group); both rules are active
#   groups       every directory between the root and a cluster is a group
#                level: group.values.* and apps/ of each level reach every
#                cluster below it, outer level first
#
# Fixture root tests/fixtures-markers:
#   global.values.yaml, apps/g-app
#   region/            group: group.values.yaml, apps/r-app (+ values.yaml)
#     eu/              group: group.values.yaml, apps/eu-app
#       c-marker/      cluster by marker (cluster.yaml, cluster.values.yaml)
#       c-legacy/      cluster by the legacy rule (apps/own-app)
#   flat/              ungrouped cluster by marker
#
# Error roots: fixtures-markers-in-apps, fixtures-markers-nested,
# fixtures-markers-misplaced.

_root() { cd "${BATS_TEST_DIRNAME}/../../.." && pwd; }

# _helmfile runs helmfile against a fixture root ($1); further args pass through.
_helmfile() {
  local fixtures="$1" root; shift
  root="$(_root)"
  helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    --state-values-set "atlas.deploymentDefinitions=${fixtures}" "$@"
}

# _pairs prints "cluster deploymentName" per discovered pair (discovery map).
_pairs() {
  ATLAS_DISCOVERY_MAP=1 _helmfile fixtures-markers build --allow-no-matching-release 2>/dev/null \
    | yq eval-all -o=json '[.]' - \
    | jq -r '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0].pairs[] | .cluster + " " + .deploymentName' \
    | sort
}

# _value prints one value of the rendered app1 ConfigMap of a pair.
# Args: $1=cluster $2=deployment $3=yq path
_value() {
  ATLAS_FILTER_CLUSTER="$1" ATLAS_FILTER_DEPLOYMENT_NAME="$2" \
    _helmfile fixtures-markers template --skip-schema-validation -q 2>/dev/null \
    | yq "select(.kind == \"ConfigMap\") | .data.values | $3"
}

setup_file() {
  export PAIRS_FILE="${BATS_FILE_TMPDIR}/pairs.txt"
  _pairs > "$PAIRS_FILE"
  [ -s "$PAIRS_FILE" ]
}

# _deployments_of prints the deployment names of one cluster, space-separated.
_deployments_of() {
  grep "^$1 " "$PAIRS_FILE" | cut -d' ' -f2 | tr '\n' ' '
}

@test "markers: discovery finds exactly the three clusters" {
  run bash -c "cut -d' ' -f1 '$PAIRS_FILE' | sort -u | tr '\n' ' '"
  [ "$output" = "flat region/eu/c-legacy region/eu/c-marker " ]
}

@test "markers: a marker cluster without apps/ inherits from every level" {
  run _deployments_of region/eu/c-marker
  [ "$output" = "eu-app g-app r-app " ]
}

@test "markers: a legacy cluster in a nested group inherits from every level" {
  run _deployments_of region/eu/c-legacy
  [ "$output" = "eu-app g-app own-app r-app " ]
}

@test "markers: an ungrouped marker cluster gets the global deployments only" {
  run _deployments_of flat
  [ "$output" = "g-app " ]
}

@test "markers: values of all group levels reach the cluster" {
  run _value region/eu/c-marker eu-app '[.fromGlobal, .fromRegion, .fromEu] | join(",")'
  [ "$output" = "yes,yes,yes" ]
}

@test "markers: precedence global < outer group < inner group < cluster" {
  run _value region/eu/c-marker eu-app '.level'
  [ "$output" = "cluster" ]
  run _value region/eu/c-legacy own-app '.level'
  [ "$output" = "eu" ]
}

@test "markers: the own directory of an inherited deployment wins over the cluster" {
  run _value region/eu/c-marker r-app '.ownDirWins'
  [ "$output" = "r-app-dir" ]
}

@test "markers: an ungrouped cluster sees no group values" {
  run _value flat g-app '[.level, (.fromRegion // "none")] | join(",")'
  [ "$output" = "global,none" ]
}

@test "markers: cluster.yaml inside an apps/ directory fails" {
  run _helmfile fixtures-markers-in-apps build 2>&1
  [ "$status" -ne 0 ]
  [[ "$output" == *"c1/apps/x/cluster.yaml is inside an apps/ directory"* ]]
}

@test "markers: a marker cluster with clusters below it fails" {
  run _helmfile fixtures-markers-nested build 2>&1
  [ "$status" -ne 0 ]
  [[ "$output" == *"outer has a cluster.yaml, but clusters are below it: outer/inner"* ]]
}

@test "markers: deployment.yaml outside apps/ fails in an unfiltered render" {
  run _helmfile fixtures-markers-misplaced build 2>&1
  [ "$status" -ne 0 ]
  [[ "$output" == *"deployment.yaml outside an apps/ directory"*"c1/stray/deployment.yaml"* ]]
}

@test "markers: the apps/ convention check is skipped in a filtered render" {
  ATLAS_FILTER_CLUSTER=c1 ATLAS_FILTER_DEPLOYMENT_NAME=ok run _helmfile fixtures-markers-misplaced build 2>&1
  [ "$status" -eq 0 ]
}
