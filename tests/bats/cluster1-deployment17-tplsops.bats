#!/usr/bin/env bats
#
# Scenario: an app template ships SOPS-encrypted default values alongside its
# plain defaults, so ops folks can bake in sensible secrets (DB credentials,
# infra tokens, etc.) without every consumer repo re-encrypting them. This
# fixture mixes a template-include file, an inline map, and a .sops.yaml all
# in the release's values: list — ATLAS decrypts the SOPS entry and inlines
# it, honoring list order for precedence.
#
# Fixture layout:
#   tests/templates/app-tplsops/
#     helmfile.yaml.gotmpl     values: [values.yaml.gotmpl, inline map, values.sops.yaml]
#     values.yaml.gotmpl       plain template-include default
#     values.sops.yaml         SOPS-encrypted template-level defaults (listed last)
#
# Assertions:
#   - Plain template-include values reach the rendered output (baseline).
#   - Template-inline defaults reach the output.
#   - SOPS-encrypted template values are decrypted and appear in output.
#   - Nested SOPS maps decrypt correctly.
#   - SOPS (last in the values list) overrides an earlier inline default of
#     the same key.

load 'helpers/render'

setup_file() {
  ensure_rendered
  ensure_rendered_redacted
}

@test "plain template-include value reaches output" {
  run get_path cluster1 deployment17 app-tplsops .tplInclude
  [ "$output" = "fromInclude" ]
}

@test "template-inline default reaches output" {
  run get_path cluster1 deployment17 app-tplsops .tplDefault
  [ "$output" = "fromDefault" ]
}

@test "SOPS-encrypted template secret is decrypted" {
  run get_path cluster1 deployment17 app-tplsops .tplSopsSecret
  [ "$output" = "fromTemplateSops" ]
}

@test "SOPS-encrypted nested map decrypts correctly" {
  run get_path cluster1 deployment17 app-tplsops .tplSopsMap.username
  [ "$output" = "tplUser" ]
  run get_path cluster1 deployment17 app-tplsops .tplSopsMap.token
  [ "$output" = "s3cret" ]
}

@test "SOPS value overrides a template-inline value of the same key" {
  run get_path cluster1 deployment17 app-tplsops .tplSopsOverride
  [ "$output" = "fromSops" ]
}

# --- Redaction: template-level SOPS values -----------------------------------
#
# Template SOPS values must flow into the atlas-redact post-renderer's
# replacement map. Without that, decrypted secrets from the template's
# values.sops.yaml would leak verbatim in the redacted render while hierarchy
# SOPS values are redacted — a confusing half-redacted state.

@test "redact d17: template SOPS scalar (tplSopsSecret) redacted" {
  # "fromTemplateSops" (16 alphanumeric) → first 8 of "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsSecret
  [ "$output" = "REDACTED" ]
}

@test "redact d17: template SOPS override value redacted" {
  # "fromSops" (8 alphanumeric) → "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsOverride
  [ "$output" = "REDACTED" ]
}

@test "redact d17: template SOPS nested map.username redacted" {
  # "tplUser" (7 alphanumeric) → first 7 of "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsMap.username
  [ "$output" = "REDACTE" ]
}

@test "redact d17: template SOPS nested map.token redacted" {
  # "s3cret" (6 alphanumeric) → first 6 of "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsMap.token
  [ "$output" = "REDACT" ]
}

@test "redact d17: plain template-include value preserved" {
  # "fromInclude" is not SOPS-derived — must not be rewritten.
  run get_path_redacted cluster1 deployment17 app-tplsops .tplInclude
  [ "$output" = "fromInclude" ]
}
