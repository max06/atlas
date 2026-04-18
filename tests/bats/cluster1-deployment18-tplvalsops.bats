#!/usr/bin/env bats
#
# Scenario: an app template declares a SOPS-encrypted file directly in the
# release's values: list (not the secrets: field). ATLAS decrypts the file
# eagerly and inlines the content as a regular values source, so downstream
# consumers (helm, patch gotmpl rendering, chained .yaml.gotmpl files) see
# the decrypted values without relying on helmfile's internal ordering
# between values: and secrets: — an ordering the project has been bitten by
# before.
#
# Fixture layout:
#   tests/templates/app-tplvalsops/
#     helmfile.yaml.gotmpl     values: [values.sops.yaml, <inline map>]
#     values.sops.yaml         SOPS-encrypted template-level defaults
#
# Assertions:
#   - SOPS entries in values: are decrypted and their keys land in output.
#   - Nested SOPS structures decrypt correctly.
#   - The values: list honors position: an inline map after the SOPS file
#     overrides same-key entries from the SOPS file.

load 'helpers/render'

setup_file() {
  ensure_rendered
  ensure_rendered_redacted
}

@test "SOPS entry in values: list is decrypted and inlined" {
  run get_path cluster1 deployment18 app-tplvalsops .tplValsSecret
  [ "$output" = "fromValsSops" ]
}

@test "SOPS-decrypted nested map is available under its top-level key" {
  run get_path cluster1 deployment18 app-tplvalsops .tplValsNested.apiKey
  [ "$output" = "ak-ops-baseline" ]
  run get_path cluster1 deployment18 app-tplvalsops .tplValsNested.token
  [ "$output" = "ops-token-xyz" ]
}

@test "Inline entry after SOPS still lands in output" {
  run get_path cluster1 deployment18 app-tplvalsops .tplValsInline
  [ "$output" = "fromInline" ]
}

@test "Later entries override earlier SOPS entries in the values: list" {
  run get_path cluster1 deployment18 app-tplvalsops .tplValsOverride
  [ "$output" = "inline" ]
}

# --- Redaction: SOPS values inlined from values: list ------------------------
#
# When ATLAS eagerly decrypts .sops.yaml entries in the values: list, the
# decrypted leaves must also be registered with the atlas-redact post-renderer
# so they get redacted in the redacted render. Values that were overridden by
# later non-SOPS entries (or that never originated from SOPS) must remain
# unchanged in the redacted output.

@test "redact d18: inlined SOPS scalar (tplValsSecret) redacted" {
  # "fromValsSops" (12 alphanumeric) → first 8 of "REDACTED"
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsSecret
  [ "$output" = "REDACTED" ]
}

@test "redact d18: inlined SOPS nested map.apiKey redacted" {
  # "ak-ops-baseline" → "ak" → "RE", "ops" → "RED", "baseline" → "REDACTED"
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsNested.apiKey
  [ "$output" = "RE-RED-REDACTED" ]
}

@test "redact d18: inlined SOPS nested map.token redacted" {
  # "ops-token-xyz" → "ops" → "RED", "token" → "REDAC", "xyz" → "RED"
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsNested.token
  [ "$output" = "RED-REDAC-RED" ]
}

@test "redact d18: plain inline value preserved" {
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsInline
  [ "$output" = "fromInline" ]
}

@test "redact d18: SOPS value overridden by inline stays as inline (not redacted)" {
  # SOPS said "fromSops" but an inline entry later in values: set it to "inline".
  # "inline" is not SOPS-derived in the final rendered output, so no replacement.
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsOverride
  [ "$output" = "inline" ]
}
