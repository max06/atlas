#!/usr/bin/env bats
#
# Scenario: state-build never decrypts SOPS files.
#
# Stage-2 (helmfile.single) and stage-3 state-build (helmfile.instance) walk
# the hierarchy with skipSecrets=true: *.sops.yaml files contribute their
# plaintext KEY structure (values stay ENC[...]), and decryption happens
# exclusively in the release-time values-loader. The observable contract:
# `helmfile build` on a remote-chart deployment — which renders the full
# state but not the release values — must succeed with NO sops key material
# available at all. (Unselected deployments still fan out through stage-2,
# so this exercises the skipSecrets path for every deployment in the tree.)
#
# Scoped to deployment1 because local-chart releases (chartify) legitimately
# render their values-loader at build time and thus still need the key.
#
# Regression guard for the eager-decrypt behaviour where every render
# decrypted every deployment's hierarchy at fan-out time (one sops call per
# deployment per render, even for unselected deployments) — without
# skipSecrets this build fails on the first hierarchy sops file.

_root() {
  cd "$(dirname "$(readlink -f "${BATS_TEST_FILENAME}")")"/../../.. && pwd
}

_keyless_build() {
  # Point every sops/age lookup path at nothing: no env key, no key file,
  # and an empty HOME so ~/.config/sops/age/keys.txt cannot leak in.
  # gnupg is not touched — the test fixtures are age-encrypted only.
  local emptyhome="${BATS_TEST_TMPDIR}/nohome"
  mkdir -p "$emptyhome"
  env -u SOPS_AGE_KEY -u SOPS_AGE_KEY_FILE HOME="$emptyhome" \
    helmfile -f "$(_root)/tests/helmfile.yaml.gotmpl" build \
    --selector cluster=cluster1,deploymentName=deployment1
}

@test "state-build succeeds without any sops key material" {
  run _keyless_build
  [ "$status" -eq 0 ]
}

@test "keyless-built state still defers values to the values-loader" {
  run _keyless_build
  [ "$status" -eq 0 ]
  [[ "$output" == *"helmfile.values-loader.yaml.gotmpl"* ]]
}
