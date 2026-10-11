#!/usr/bin/env bats
#
# Scenario: ATLAS_SKIP_SECRETS=true renders without decrypting any SOPS file.
#
# The release-time values-loader normally decrypts every *.sops.yaml it
# reads. With ATLAS_SKIP_SECRETS=true it reads them as plain YAML instead:
# keys merge with their ENC[...] ciphertext as values, and no SOPS key
# material is needed at all. Covers every place a SOPS file can enter a
# release:
#   - hierarchy files      global/cluster/deployment values.sops.yaml (d1)
#   - values: list entry   template-level values.sops.yaml (d18)
#   - release.secrets      template-secret.sops.yaml (d38)
#   - apps[].secrets       instance-secret.sops.yaml (d38)
#
# Every render here runs KEYLESS (no SOPS_AGE_KEY*, empty HOME), so any
# decryption attempt fails the render. The control test proves the keyless
# setup really breaks a normal render.

load '../helpers/render'

SKIP_RENDER_DIR="${BATS_RUN_TMPDIR:-/tmp}/atlas-bats-render-skip-secrets"

# _keyless runs a command with every sops/age key lookup path pointed at
# nothing: no env key, no key file, and an empty HOME so
# ~/.config/sops/age/keys.txt cannot leak in. gnupg is not touched — the
# test fixtures are age-encrypted only.
_keyless() {
  local emptyhome="${BATS_RUN_TMPDIR:-/tmp}/atlas-bats-nohome"
  mkdir -p "$emptyhome"
  env -u SOPS_AGE_KEY -u SOPS_AGE_KEY_FILE HOME="$emptyhome" "$@"
}

setup_file() {
  local root
  root="$(_repo_root)"
  rm -rf "$SKIP_RENDER_DIR"
  ATLAS_SKIP_SECRETS=true _keyless \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
      template --skip-schema-validation \
      --selector cluster=cluster1 \
      --output-dir "$SKIP_RENDER_DIR" \
      --output-dir-template '{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}' \
      > "${SKIP_RENDER_DIR}.log" 2>&1 \
    || { echo "keyless ATLAS_SKIP_SECRETS render failed. log:" >&2; \
         cat "${SKIP_RENDER_DIR}.log" >&2; return 1; }
}

# skip_path extracts one yq path from a release's values in the
# ATLAS_SKIP_SECRETS render. Args: $1=deployment $2=release $3=yq path.
skip_path() {
  local cm_name="${2}-chart1"
  yq "select(.kind == \"ConfigMap\" and .metadata.name == \"${cm_name}\") | .data.values | $3" \
    "$SKIP_RENDER_DIR"/cluster1/"$1"/"$2"/chart1/templates/*.yaml
}

# sops_ciphertext reads the raw ENC[...] string of a key from a fixture.
sops_ciphertext() {
  yq "$2" "$(_repo_root)/tests/$1"
}

@test "control: keyless render WITHOUT ATLAS_SKIP_SECRETS fails" {
  run _keyless helmfile -f "$(_repo_root)/tests/helmfile.yaml.gotmpl" \
    template --skip-schema-validation \
    --selector cluster=cluster1,deploymentName=deployment1
  [ "$status" -ne 0 ]
}

@test "hierarchy: global SOPS value stays ciphertext" {
  run skip_path deployment1 app1 .sopsGlobal
  [ "$output" = "$(sops_ciphertext deployments/global.values.sops.yaml .sopsGlobal)" ]
  [[ "$output" == ENC\[AES256_GCM,* ]]
}

@test "hierarchy: nested SOPS keys keep their structure" {
  run skip_path deployment1 app1 .sopsNested.secretKey
  [[ "$output" == ENC\[AES256_GCM,* ]]
}

@test "hierarchy: sops metadata block does not reach the values" {
  run skip_path deployment1 app1 'has("sops")'
  [ "$output" = "false" ]
}

@test "hierarchy: plain values are unaffected" {
  run skip_path deployment1 app1 .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "values: list SOPS entry stays ciphertext, inline entry unaffected" {
  run skip_path deployment18 app-tplvalsops .tplValsSecret
  [[ "$output" == ENC\[AES256_GCM,* ]]
  run skip_path deployment18 app-tplvalsops .tplValsInline
  [ "$output" = "fromInline" ]
}

@test "release.secrets: template secret stays ciphertext and still wins over values" {
  run skip_path deployment38 stage3-secrets-release .key2
  [ "$output" = "$(sops_ciphertext templates/app-secrets/template-secret.sops.yaml .key2)" ]
  run skip_path deployment38 stage3-secrets-release .key1
  [ "$output" = "fromValues" ]
}

@test "apps[].secrets: instance secret stays ciphertext and still wins over template secret" {
  run skip_path deployment38 stage3-secrets-release .key3
  [ "$output" = "$(sops_ciphertext deployments/cluster1/apps/deployment38/instance-secret.sops.yaml .key3)" ]
}

@test "combined with ATLAS_REDACT_SECRETS: keyless render succeeds" {
  local root default_plugins
  root="$(_repo_root)"
  default_plugins="$(helm env HELM_PLUGINS 2>/dev/null || echo "")"
  ATLAS_SKIP_SECRETS=true ATLAS_REDACT_SECRETS=true \
  HELM_PLUGINS="${default_plugins:+${default_plugins}:}${root}/.github/actions/atlas-render" \
    run _keyless helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
      template --skip-schema-validation \
      --selector cluster=cluster1,deploymentName=deployment38
  [ "$status" -eq 0 ]
  [[ "$output" == *"ENC[AES256_GCM,"* ]]
}
