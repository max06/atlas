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

@test "native render --redact: equals the review's render step with redaction in the CLI" {
  # A: the review's render step (helmfile engine, maps from helmfile build,
  # snapshot.RedactTree) through the side contract; B: atlas render
  # --engine native --redact (maps from the native states, same scrub)
  local root; root="$(_repo_root)"
  cat > "$TEST_TEMP/req.json" <<JSON
{"contract": 1, "step": "render", "dir": "$root", "entry": "tests/helmfile.yaml.gotmpl", "label": "pr",
 "temp": "$TEST_TEMP/a", "sidedumpDir": "$TEST_TEMP/a-maps", "redactInCLI": true,
 "sopsAgeKeyFile": "$SOPS_AGE_KEY_FILE", "parallel": 4}
JSON
  mkdir -p "$TEST_TEMP/a"
  (cd "$root" && "$ATLAS_CLI" review ci side --request "$TEST_TEMP/req.json" --result "$TEST_TEMP/res.json" >/dev/null 2>&1)
  [ "$(jq -r '.render.status' "$TEST_TEMP/res.json")" = "success" ]
  (cd "$root/tests" && "$ATLAS_CLI" render --engine native --redact --output-dir "$TEST_TEMP/b" >/dev/null 2>&1)
  run diff -r "$(jq -r '.render.snapshotDir' "$TEST_TEMP/res.json")" "$TEST_TEMP/b"
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  # not vacuous: maps applied, Secrets hashed, no plaintext of a test secret
  run grep -rq 'AKIAIOSFODNN7EXAMPLE' "$TEST_TEMP/b"
  [ "$status" -eq 1 ]
  grep -rq 'REDACTED' "$TEST_TEMP/b"
}
