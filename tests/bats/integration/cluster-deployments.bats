#!/usr/bin/env bats
#
# Scenario: atlas.cluster.deployments, called from a template's values file.
# A structure template (the cluster root of a static ApplicationSet) renders
# one Application per deployment of its cluster. It gets the list from this
# helper, the same code the discovery uses (STEP 2), and the list never goes
# into .Values.atlas.
#
# Fixture root tests/fixtures-cluster-deployments: apps/global-a,
# g/apps/group-b, g/c1/apps/{own-c, probe}; probe uses the template
# app-cluster-deployments, which writes the helper result into its values.

_root() { cd "${BATS_TEST_DIRNAME}/../../.." && pwd; }

_helmfile() {
  local root
  root="$(_root)"
  helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    --state-values-set "atlas.deploymentDefinitions=fixtures-cluster-deployments" "$@"
}

@test "cluster deployments: the helper lists every deployment of the cluster, sorted" {
  local root output_names
  root="$(_root)"
  output_names="$(ATLAS_FILTER_CLUSTER=g/c1 ATLAS_FILTER_DEPLOYMENT_NAME=probe \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" --state-values-set atlas.deploymentDefinitions=fixtures-cluster-deployments \
    template --skip-schema-validation -q 2>/dev/null \
    | yq 'select(.kind == "ConfigMap") | .data.values.clusterDeployments[].deploymentName' | tr '\n' ' ')"
  [ "$output_names" = "global-a group-b own-c probe " ]
}

@test "cluster deployments: the helper agrees with the discovery map" {
  local root map_names helper_names
  root="$(_root)"
  map_names="$(ATLAS_DISCOVERY_MAP=1 helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    --state-values-set atlas.deploymentDefinitions=fixtures-cluster-deployments \
    build --allow-no-matching-release 2>/dev/null | yq eval-all -o=json '[.]' - \
    | jq -r '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0].pairs[] | select(.cluster == "g/c1") | .deploymentName' | sort | tr '\n' ' ')"
  helper_names="$(ATLAS_FILTER_CLUSTER=g/c1 ATLAS_FILTER_DEPLOYMENT_NAME=probe \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" --state-values-set atlas.deploymentDefinitions=fixtures-cluster-deployments \
    template --skip-schema-validation -q 2>/dev/null \
    | yq 'select(.kind == "ConfigMap") | .data.values.clusterDeployments[].deploymentName' | tr '\n' ' ')"
  [ -n "$map_names" ]
  [ "$map_names" = "$helper_names" ]
}

@test "cluster deployments: paths point at the deployment.yaml files" {
  local root paths
  root="$(_root)"
  paths="$(ATLAS_FILTER_CLUSTER=g/c1 ATLAS_FILTER_DEPLOYMENT_NAME=probe \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" --state-values-set atlas.deploymentDefinitions=fixtures-cluster-deployments \
    template --skip-schema-validation -q 2>/dev/null \
    | yq 'select(.kind == "ConfigMap") | .data.values.clusterDeployments[] | select(.deploymentName == "group-b") | .path')"
  [[ "$paths" == */tests/fixtures-cluster-deployments/g/apps/group-b/deployment.yaml ]]
}
