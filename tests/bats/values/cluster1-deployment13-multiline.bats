#!/usr/bin/env bats
#
# Scenario: multi-line string values from every source format round-trip
# through the ATLAS pipeline intact, without corrupting the store trace that
# is emitted as YAML comments before the sub-helmfile document.
#
# Without the fix in templates/_store.tpl (formatValue now escapes \n and \r),
# a multi-line value lands directly into a "#" comment line, and continuation
# lines drop into the surrounding YAML document as raw data — breaking the
# whole render.
#
# The fixture at tests/deployments/cluster1/apps/deployment13/ exercises all
# three source formats that can deliver multi-line strings:
#   1. values.yaml        — plain YAML block scalar
#   2. values.yaml.gotmpl — gotmpl-rendered block scalar (with interpolation)
#   3. values.sops.yaml   — SOPS-encrypted block scalar (fake PEM key)

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment13
INSTANCE=app-novals

setup_file() { ensure_rendered; }

@test "d13: multi-line plain yaml value arrives intact" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .multilinePlain
  [[ "$output" == *"first line from plain yaml"* ]]
  [[ "$output" == *"second line"* ]]
  [[ "$output" == *"third line"* ]]
}

@test "d13: multi-line gotmpl value arrives with interpolation applied" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .multilineGotmpl
  # Confirms both (a) the gotmpl ran (globalOnly / clusterOnly got substituted)
  # and (b) newlines survived the round trip.
  [[ "$output" == *"line one renders fromGlobal"* ]]
  [[ "$output" == *"line two renders fromCluster"* ]]
  [[ "$output" == *"line three is static"* ]]
}

@test "d13: multi-line SOPS value decrypted and delivered intact" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .multilineSecret
  [[ "$output" == *"-----BEGIN FAKE PRIVATE KEY-----"* ]]
  [[ "$output" == *"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDtestkeyonlyfortesting"* ]]
  [[ "$output" == *"-----END FAKE PRIVATE KEY-----"* ]]
}
