#!/usr/bin/env bats
#
# Scenario: template-render-time hierarchy access.
#
# When ATLAS reads an app template's helmfile.yaml.gotmpl in stage-3, it
# renders the file as a Go template before iterating its releases:[].
# Anything in the template body using `{{ .Values.<hierarchyKey> }}` must
# resolve to the merged hierarchy (global → group → cluster → deployment),
# matching stage-2's contract. This is what consumers rely on for sops-
# token injection into jsonPatches, hierarchy-derived chart paths, and
# inline values maps that pull from cluster-specific config.
#
# Without the hierarchy in template-render context, every such reference
# becomes empty silently and downstream patches produce nonsense or
# fail to match.

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment41
RELEASE=stage3-tplhier-release

setup_file() { ensure_rendered; }

@test "d41: inline values map resolves a hierarchy key at template-render time" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .inlineEcho
  [ "$output" = "fromGlobal" ]
}

@test "d41: inline values map resolves a NESTED hierarchy key at template-render time" {
  # nested.shared is set in global.values.yaml ("global") and overridden in
  # cluster1/cluster.values.yaml ("cluster") — confirm the deeper level wins
  # at template-render time, proving the FULL merged hierarchy is in scope.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .nestedEcho
  [ "$output" = "cluster" ]
}

@test "d41: jsonPatches inline patch resolves a hierarchy key at template-render time" {
  dir="$(_release_dir "$CLUSTER" "$DEPLOYMENT" "$RELEASE")"
  run yq 'select(.kind == "ConfigMap" and .metadata.name == "stage3-tplhier-release-chart1") | .metadata.annotations["atlas-test/from-hierarchy"]' \
    "$dir"/chart1/templates/*.yaml
  [ "$output" = "fromGlobal" ]
}
