#!/usr/bin/env bats
#
# Scenario: stage-3 with template-level SOPS in the release's values: list.
# Same app-tplsops template that deployment17 exercises via stage-2 — the
# values: list is [.yaml.gotmpl, inline map, .sops.yaml]. The stage-3 loader
# must decrypt the .sops.yaml entry and merge it in list-order so list
# precedence (later wins) holds.
#
# Redaction is deferred to a later milestone; only plain-render assertions
# here.

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment34
RELEASE=app-tplsops

setup_file() {
  ensure_rendered
  ensure_rendered_redacted
}

@test "d34: plain template-include value reaches output" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplInclude
  [ "$output" = "fromInclude" ]
}

@test "d34: template-inline default reaches output" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplDefault
  [ "$output" = "fromDefault" ]
}

@test "d34: SOPS-encrypted template secret decrypts" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsSecret
  [ "$output" = "fromTemplateSops" ]
}

@test "d34: SOPS-encrypted nested map decrypts" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsMap.username
  [ "$output" = "tplUser" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsMap.token
  [ "$output" = "s3cret" ]
}

@test "d34: SOPS value overrides template-inline same key (list order)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsOverride
  [ "$output" = "fromSops" ]
}

# --- Redaction: template-level SOPS values must flow through atlas-redact -

@test "redact d34: template SOPS scalar (tplSopsSecret) redacted" {
  # "fromTemplateSops" (16 alphanumeric) → first 8 chars of "REDACTED"
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsSecret
  [ "$output" = "REDACTED" ]
}

@test "redact d34: template SOPS override value redacted" {
  # "fromSops" (8 alphanumeric) → "REDACTED"
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsOverride
  [ "$output" = "REDACTED" ]
}

@test "redact d34: template SOPS nested map.username redacted" {
  # "tplUser" (7 alphanumeric) → first 7 chars of "REDACTED"
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsMap.username
  [ "$output" = "REDACTE" ]
}

@test "redact d34: template SOPS nested map.token redacted" {
  # "s3cret" (6 alphanumeric) → first 6 chars of "REDACTED"
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplSopsMap.token
  [ "$output" = "REDACT" ]
}

@test "redact d34: plain template-include value preserved" {
  # tplInclude is not SOPS-derived — must not be rewritten.
  run get_path_redacted "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tplInclude
  [ "$output" = "fromInclude" ]
}
