#!/usr/bin/env bats
#
# Scenario: strategicMergePatches path resolution + patch application.
#
# App templates often ship a sibling /patches directory; the helmfile
# release references it with a relative path (./patches/foo.yaml). ATLAS
# rewrites these paths to absolute (template-relative) so helmfile can
# locate them regardless of the consumer's CWD, then helmfile applies the
# patches to the rendered manifests.
#
# This test verifies BOTH the path-resolution wiring AND that helmfile
# actually applies the patch (the rendered ConfigMap carries the
# annotation introduced by the patch).

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment39
RELEASE=stage3-patches-release

setup_file() { ensure_rendered; }

@test "d39: release rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d39: strategicMergePatch applied — ConfigMap has the annotation" {
  dir="$(_release_dir "$CLUSTER" "$DEPLOYMENT" "$RELEASE")"
  run yq 'select(.kind == "ConfigMap" and .metadata.name == "stage3-patches-release-chart1") | .metadata.annotations["atlas-test/patched"]' \
    "$dir"/chart1/templates/*.yaml
  [ "$output" = "yes" ]
}

@test "d39: helmfile build state has absolute path for the patch" {
  local root
  root="$(_repo_root)"
  run bash -c "helmfile -f '$root/tests/helmfile.yaml.gotmpl' \
    build --selector cluster=$CLUSTER,deploymentName=$DEPLOYMENT 2>/dev/null \
    | yq 'select(.releases != null) | .releases[] | select(.name == \"$RELEASE\") | .strategicMergePatches[0]'"
  # Path must be absolute (start with /) and point inside the template dir.
  # convertPaths preserves the original `./` prefix as `/./` in the joined
  # output — semantically identical to the cleaned form, kustomize accepts
  # either. A future refactor of convertPaths can normalize this.
  [[ "$output" == /*tests/templates/app-patches/*patches/cm-patch.yaml ]]
}
