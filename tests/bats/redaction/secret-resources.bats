#!/usr/bin/env bats
#
# Scenario: structural redaction of v1/Secret resources (cluster1/deployment47,
# template app-secret-resource). Unlike the replacement-map tests in
# redaction.bats, the values here (except fromSops) never touched SOPS — the
# chart-generated-cert / random-password class. The atlas-redact post-renderer
# must:
#   - be wired even though this release's replacement map is EMPTY,
#   - replace every data/stringData value with the deterministic marker
#     "REDACTED:sha256:<first-12-hex-of-value>",
#   - hash the REAL value even when it is also SOPS-tainted (structural pass
#     runs before the map pass — a map-first order would hash the redacted
#     shape and hide rotations),
#   - leave the same values readable OUTSIDE Secret documents (per-document
#     scope, not a global value hunt),
#   - leave Secrets untouched in the non-redacted render.

load '../helpers/render'

setup_file() {
  ensure_rendered
  ensure_rendered_redacted
}

# Rendered manifest paths for the secret-app release (chart name chart-secret).
_secret_yaml_redacted() {
  echo "${RENDER_DIR_REDACTED}/cluster1/deployment47/secret-app/chart-secret/templates/secret.yaml"
}
_secret_yaml_plain() {
  echo "${RENDER_DIR}/cluster1/deployment47/secret-app/chart-secret/templates/secret.yaml"
}

# _marker computes the expected redaction marker for a raw value.
_marker() {
  printf 'REDACTED:sha256:%s' "$(printf '%s' "$1" | sha256sum | head -c 12)"
}

# _b64 encodes without trailing newline/wrapping (values are short).
_b64() {
  printf '%s' "$1" | base64 | tr -d '\n'
}

@test "secret-redact: data value (chart-b64-encoded plain value) becomes marker" {
  expected="$(_marker "$(_b64 'fake-cert-payload-AAAA')")"
  run yq 'select(.kind == "Secret") | .data.generatedCert' "$(_secret_yaml_redacted)"
  [ "$output" = "$expected" ]
}

@test "secret-redact: stringData plain value becomes marker" {
  expected="$(_marker 'not-a-secret')"
  run yq 'select(.kind == "Secret") | .stringData.note' "$(_secret_yaml_redacted)"
  [ "$output" = "$expected" ]
}

@test "secret-redact: SOPS-tainted stringData value hashes the REAL value (pass order)" {
  # Decrypt the fixture SOPS file to learn the real value, then expect its
  # marker. If the map pass ran first, the marker would hash "REDACTED"
  # instead and this assertion would fail.
  root="$(_repo_root)"
  sops_plain="$(SOPS_AGE_KEY_FILE="$root/tests/sops-secret.txt" \
    sops -d --extract '["sopsGlobal"]' "$root/tests/deployments/global.values.sops.yaml")"
  [ -n "$sops_plain" ]
  expected="$(_marker "$sops_plain")"
  run yq 'select(.kind == "Secret") | .stringData.fromSops' "$(_secret_yaml_redacted)"
  [ "$output" = "$expected" ]
  # And never the marker of the map-redacted shape:
  [ "$output" != "$(_marker 'REDACTED')" ]
}

@test "secret-redact: same value outside a Secret stays readable (document scope)" {
  run yq 'select(.kind == "ConfigMap") | .data.note' "$(_secret_yaml_redacted)"
  [ "$output" = "not-a-secret" ]
}

@test "secret-redact: no raw secret bytes anywhere in the redacted manifest" {
  ! grep -q 'fake-cert-payload-AAAA' "$(_secret_yaml_redacted)"
  ! grep -qF "$(_b64 'fake-cert-payload-AAAA')" "$(_secret_yaml_redacted)"
}

@test "secret-redact: non-redacted render leaves the Secret untouched" {
  run yq 'select(.kind == "Secret") | .data.generatedCert' "$(_secret_yaml_plain)"
  [ "$output" = "$(_b64 'fake-cert-payload-AAAA')" ]
}
