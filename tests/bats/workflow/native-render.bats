#!/usr/bin/env bats
# Render-level corner cases of the native engine against the template engine
# (fixtures-native-render): .Release at release time as helmfile prepares it
# (commonLabels over release labels, the kubeContext default of helmDefaults,
# release fields rendered at load time), two releases with one name in two
# namespaces, a map-shaped strategicMergePatches. The same helmfile call runs
# through the real template engine and through the native-engine shim; the
# outputs must be identical. Needs ATLAS_CLI.

load '../helpers/subset'

setup() {
  [ -n "${ATLAS_CLI:-}" ] || skip "ATLAS_CLI not set"
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/native-render.XXXXXX")"
  SHIM_DIR="$(_repo_root)/tests/bats/helpers/native-engine"
}

_render() {
  helmfile -f "$(_repo_root)/tests/helmfile.yaml.gotmpl" template --skip-schema-validation \
    --state-values-set "atlas.deploymentDefinitions=fixtures-native-render/deployments,atlas.appTemplates=fixtures-native-render/templates" 2>/dev/null
}

@test "native render: equals the template engine on the render corner cases" {
  ATLAS_NATIVE_SHIM_OFF=1 PATH="$SHIM_DIR:$PATH" _render > "$TEST_TEMP/template.yaml"
  PATH="$SHIM_DIR:$PATH" _render > "$TEST_TEMP/native.yaml"
  [ -s "$TEST_TEMP/template.yaml" ]
  run diff "$TEST_TEMP/template.yaml" "$TEST_TEMP/native.yaml"
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  # not vacuous: the cases are in the output
  grep -q 'cluster: g1/c1' "$TEST_TEMP/native.yaml"
  grep -q 'kubeContext: kc-default' "$TEST_TEMP/native.yaml"
  grep -q 'namespace: c1-ns' "$TEST_TEMP/native.yaml"
  grep -q 'namespace: nsb' "$TEST_TEMP/native.yaml"
  grep -q 'patched: "yes"' "$TEST_TEMP/native.yaml"
}
