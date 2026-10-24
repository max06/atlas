#!/usr/bin/env bats
#
# Scenario: a child render (recursive ATLAS). A parent deployment renders an
# Argo CD Application that calls the atlas plugin again; the plugin env
# carries the context of the child:
#
#   ATLAS_DEPLOYMENT               the deployment definition (deployment.yaml content)
#   ATLAS_DEPLOYMENT_BASE          directory of the deployment-level files,
#                                  relative to atlas.cwd (values.yaml,
#                                  values.yaml.gotmpl, values.sops.yaml)
#   ATLAS_FILTER_CLUSTER           the one target cluster
#   ATLAS_FILTER_DEPLOYMENT_NAME   the one deployment name
#
# Contract: a child whose definition and base equal an existing deployment
# renders EXACTLY like that deployment from its file. The definition never
# travels through .Values.atlas (it would reach the charts and change every
# child render), so the comparison is byte for byte.

load '../helpers/render'

# render_file renders one discovered pair from its deployment.yaml (stage-1
# filter only). stdout = manifests; stderr is dropped.
render_file() {
  local root cluster="$1" deployment="$2"
  root="$(_repo_root)"
  ATLAS_FILTER_CLUSTER="$cluster" ATLAS_FILTER_DEPLOYMENT_NAME="$deployment" \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" template \
    --skip-schema-validation -q 2>/dev/null
}

# render_child renders the same pair as a child: definition from a string,
# base directory given (relative to atlas.cwd = tests/).
render_child() {
  local root cluster="$1" deployment="$2" base="$3" definition="$4"
  root="$(_repo_root)"
  ATLAS_DEPLOYMENT="$definition" ATLAS_DEPLOYMENT_BASE="$base" \
    ATLAS_FILTER_CLUSTER="$cluster" ATLAS_FILTER_DEPLOYMENT_NAME="$deployment" \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" template \
    --skip-schema-validation -q 2>&1
}

# discovery_pairs prints "cluster deploymentName deploymentPath" per pair
# (deploymentPath relative to atlas.cwd).
discovery_pairs() {
  local root
  root="$(_repo_root)"
  ATLAS_DISCOVERY_MAP=1 helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    build --allow-no-matching-release 2>/dev/null \
    | yq eval-all -o=json '[.]' - \
    | jq -r '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0].pairs[] | .cluster + " " + .deploymentName + " " + .deploymentPath'
}

setup_file() {
  export PAIRS_FILE="${BATS_FILE_TMPDIR}/pairs.txt"
  discovery_pairs > "$PAIRS_FILE"
  [ -s "$PAIRS_FILE" ]
}

@test "child render: every discovered pair renders identically as a child" {
  local root failures=0 compared=0
  root="$(_repo_root)"
  while read -r cluster deployment path; do
    local file_out child_out file_rc child_rc
    file_out="$(render_file "$cluster" "$deployment")"; file_rc=$?
    child_out="$(ATLAS_DEPLOYMENT="$(cat "$root/tests/$path")" ATLAS_DEPLOYMENT_BASE="$(dirname "$path")" \
      ATLAS_FILTER_CLUSTER="$cluster" ATLAS_FILTER_DEPLOYMENT_NAME="$deployment" \
      helmfile -f "$root/tests/helmfile.yaml.gotmpl" template --skip-schema-validation -q 2>/dev/null)"; child_rc=$?
    compared=$((compared + 1))
    if [ "$file_rc" != "$child_rc" ] || [ "$file_out" != "$child_out" ]; then
      echo "MISMATCH $cluster/$deployment (rc file=$file_rc child=$child_rc)"
      diff <(echo "$file_out") <(echo "$child_out") | head -20
      failures=$((failures + 1))
    fi
  done < "$PAIRS_FILE"
  echo "compared $compared pairs, $failures mismatches"
  [ "$compared" -gt 0 ]
  [ "$failures" -eq 0 ]
}

@test "child render: the definition comes from ATLAS_DEPLOYMENT, not from the file" {
  local root definition
  root="$(_repo_root)"
  definition="$(sed 's/namespace: test/namespace: from-child-env/' "$root/tests/deployments/cluster1/apps/deployment1/deployment.yaml")"
  run render_child cluster1 deployment1 deployments/cluster1/apps/deployment1 "$definition"
  [ "$status" -eq 0 ]
  [[ "$output" == *"namespace: from-child-env"* ]]
}

@test "child render: works without a deployment.yaml on disk (virtual deployment)" {
  # deployments/cluster1 has values files but no deployment.yaml: the child
  # path deployments/cluster1/deployment.yaml does not exist.
  local root
  root="$(_repo_root)"
  [ ! -e "$root/tests/deployments/cluster1/deployment.yaml" ]
  run render_child cluster1 virtual-child deployments/cluster1 $'apps:\n  - template: app1\n    namespace: virtual'
  [ "$status" -eq 0 ]
  [[ "$output" == *"deploymentName: virtual-child"* ]]
  [[ "$output" == *"namespace: virtual"* ]]
}

@test "child render: fails without ATLAS_DEPLOYMENT_BASE" {
  run render_child cluster1 deployment1 "" "apps: []"
  [ "$status" -ne 0 ]
  [[ "$output" == *"ATLAS_DEPLOYMENT_BASE must name the directory"* ]]
}

@test "child render: fails when ATLAS_DEPLOYMENT_BASE leaves the repository" {
  run render_child cluster1 deployment1 "../../etc" "apps: []"
  [ "$status" -ne 0 ]
  [[ "$output" == *"ATLAS_DEPLOYMENT_BASE leaves the repository: ../../etc"* ]]
}

@test "child render: fails with more than one cluster in the filter" {
  run render_child "cluster1,group1/cluster2" deployment1 deployments/cluster1/apps/deployment1 "apps: []"
  [ "$status" -ne 0 ]
  [[ "$output" == *"must name exactly one cluster and one deployment"* ]]
}

@test "child render: fails for an unknown cluster" {
  run render_child does-not-exist deployment1 deployments/cluster1/apps/deployment1 "apps: []"
  [ "$status" -ne 0 ]
  [[ "$output" == *'cluster "does-not-exist" not found'* ]]
}

@test "child render: the discovery map lists the childRender capability" {
  local root
  root="$(_repo_root)"
  run bash -c "ATLAS_DISCOVERY_MAP=1 helmfile -f '$root/tests/helmfile.yaml.gotmpl' build --allow-no-matching-release 2>/dev/null | yq eval-all -o=json '[.]' - | jq -r '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0].capabilities[]'"
  [ "$status" -eq 0 ]
  [[ "$output" == *"childRender"* ]]
}
