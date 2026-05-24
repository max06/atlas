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

load '../helpers/render'

setup_file() { ensure_rendered; }

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

