#!/usr/bin/env bats
#
# Scenario: template-level multi-line secrets — the pattern that leaked in
# v0.1.0 and was fixed by the "Split rendering" commit (twin-load extended to
# per-release values: lists). Two sub-cases:
#
#   1. A SOPS file in the app template's values: list carries a multi-line
#      block scalar directly (e.g. a PEM key or full credentials blob stored
#      as one value). atlas.redact.string produces a structure-preserving
#      redacted mirror; the diff emits {real_block: redacted_block} as a
#      whole-scalar pair; the post-renderer's yq whole-scalar match swaps it.
#
#   2. A template-level values.yaml.gotmpl composes a multi-line block
#      (AWS-credentials format) by interpolating individual SOPS leaves.
#      Twin-load renders the gotmpl twice — once with real leaves, once
#      with redacted ones — so the redacted tree already has the whole
#      redacted block. Diff emits a whole-scalar pair. Literal prefixes
#      in the gotmpl (e.g. "aws_access_key_id = ") stay intact because
#      the gotmpl contains them verbatim; only the interpolations flip.
#
# Both cases failed in v0.1.0 because the hierarchy-only twin-load never saw
# template-level values: entries. The real-world regression that motivated
# this test was an external-dns Secret whose `credentials` stringData blob
# leaked while other secrets in the same repo were redacted correctly.
#
# Fixture layout:
#   tests/templates/app-multiline-tplsops/
#     helmfile.yaml.gotmpl       values: [values.sops.yaml, credentials.yaml.gotmpl]
#     values.sops.yaml           multilineTplSecret (block scalar) +
#                                 awsAccessKeyId, awsSecretAccessKey (leaves)
#     credentials.yaml.gotmpl    awsCredentials: | block interpolating the
#                                 two SOPS leaves above (transitive taint)

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment20
INSTANCE=app-multiline-tplsops

setup_file() { ensure_rendered; }

# --- Plain render: values arrive intact -------------------------------------

@test "d20: template-level multi-line SOPS block scalar decrypts intact" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .multilineTplSecret
  [[ "$output" == *"-----BEGIN FAKE PRIVATE KEY-----"* ]]
  [[ "$output" == *"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDtplkeyonlyfortesting"* ]]
  [[ "$output" == *"-----END FAKE PRIVATE KEY-----"* ]]
}

@test "d20: template-level SOPS leaves decrypt to expected values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .awsAccessKeyId
  [ "$output" = "AKIAIOSFODNN7EXAMPLE" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .awsSecretAccessKey
  [ "$output" = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" ]
}

@test "d20: gotmpl composes multi-line credentials block from SOPS leaves" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .awsCredentials
  [[ "$output" == *"[default]"* ]]
  [[ "$output" == *"aws_access_key_id = AKIAIOSFODNN7EXAMPLE"* ]]
  [[ "$output" == *"aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"* ]]
}

