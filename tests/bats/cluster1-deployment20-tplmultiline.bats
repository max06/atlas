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

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment20
INSTANCE=app-multiline-tplsops

setup_file() {
  ensure_rendered
  ensure_rendered_redacted
}

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

# --- Redacted render: the leak that motivated this test ---------------------
#
# Every test in this block is the specific regression guard for v0.1.0's
# template-level-SOPS leak. If twin-load-per-release breaks again, any plain
# SOPS substring showing up here would be a CVE-grade leak.

@test "redact d20: template multi-line SOPS block redacted structurally" {
  # Block shape preserved (dashes, spaces, newlines), all alphanumeric runs
  # replaced with REDACTED[:min(len,8)]. Real key bytes must not appear.
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .multilineTplSecret
  [[ "$output" != *"BEGIN FAKE"* ]]
  [[ "$output" != *"MIIEvQ"* ]]
  [[ "$output" != *"tplkey"* ]]
  [[ "$output" == *"REDACTED"* ]]   # at least one full REDACTED run
  [[ "$output" == *"-----"* ]]      # dash delimiters preserved
  [[ "$output" == *$'\n'* ]]        # newlines preserved (block scalar shape)
}

@test "redact d20: template SOPS leaf awsAccessKeyId redacted" {
  # "AKIAIOSFODNN7EXAMPLE" — 20 alphanumeric, no delimiters → "REDACTED"
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .awsAccessKeyId
  [ "$output" = "REDACTED" ]
}

@test "redact d20: template SOPS leaf awsSecretAccessKey redacted" {
  # "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" — split on "/":
  #   "wJalrXUtnFEMI"       (13) → "REDACTED"
  #   "K7MDENG"              (7) → "REDACTE"
  #   "bPxRfiCYEXAMPLEKEY"  (18) → "REDACTED"
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .awsSecretAccessKey
  [ "$output" = "REDACTED/REDACTE/REDACTED" ]
}

@test "redact d20: gotmpl-composed credentials block has no plain secret bytes" {
  # The whole-scalar match pattern: twin-load renders the gotmpl with real
  # leaves in one tree and REDACTED leaves in the other, producing a diff
  # pair keyed on the full block. Post-renderer swaps the whole scalar.
  #
  # Regression guard: if this test fails, the external-dns-style leak is
  # back — someone broke template-level-release-values twin-load.
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .awsCredentials
  [[ "$output" != *"AKIAIOSFODNN7EXAMPLE"* ]]
  [[ "$output" != *"wJalrXUtnFEMI"* ]]
  [[ "$output" != *"EXAMPLEKEY"* ]]
}

@test "redact d20: gotmpl-composed credentials block preserves literals + swaps interpolations" {
  # Literal prefixes come from the gotmpl source and are identical in both
  # real/redacted renders, so the diff pair's redacted side still contains
  # them. Only the interpolated secret values (after the "= ") are redacted.
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .awsCredentials
  [[ "$output" == *"[default]"* ]]
  [[ "$output" == *"aws_access_key_id = REDACTED"* ]]
  [[ "$output" == *"aws_secret_access_key = REDACTED/REDACTE/REDACTED"* ]]
}
