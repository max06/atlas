#!/usr/bin/env bats
#
# Scenario: a template's values: list entries can reference each other. ATLAS
# progressively tpl-renders and merges each entry in declaration order, so a
# later .yaml.gotmpl file sees:
#   - keys defined by any earlier entry in the same list (plain, SOPS, inline)
#   - all hierarchy keys (as baseline context)
# Helmfile's native release-values merging is per-entry and doesn't accumulate
# context across the list; this test locks in ATLAS's progressive-merge
# semantics. Sibling fixture: cluster1-deployment17-tplsops.bats (locks in
# single-file SOPS decryption).
#
# Fixture layout:
#   tests/templates/app-crossref/
#     helmfile.yaml.gotmpl     values: [values.sops.yaml, inline map, derived.yaml.gotmpl]
#     values.sops.yaml         SOPS-encrypted: clusterdomain: foo.bar, apiToken: s3cret
#     derived.yaml.gotmpl      references earlier SOPS + inline keys AND a hierarchy key
#
# Assertions:
#   - Forward decryption (baseline) — SOPS content reaches the output.
#   - gotmpl can reference a key defined by an earlier .sops.yaml entry.
#   - gotmpl can reference keys defined by an earlier inline map.
#   - gotmpl can reference hierarchy keys at the same time as template keys.
# Plus transitive redaction: values derived from SOPS keys via a later gotmpl
# must also be redacted in the redacted render — the twin-load pattern that
# already works for hierarchy values should apply to the template list too.

load '../helpers/render'

setup_file() { ensure_rendered; }

# --- Plain render: cross-references resolve ---------------------------------

@test "d19: SOPS key from earlier entry reaches output" {
  run get_path cluster1 deployment19 app-crossref .clusterdomain
  [ "$output" = "foo.bar" ]
}

@test "d19: gotmpl references SOPS key defined earlier in the values list" {
  run get_path cluster1 deployment19 app-crossref .domainFromSops
  [ "$output" = "app.foo.bar" ]
}

@test "d19: gotmpl references plain inline keys defined earlier in the values list" {
  run get_path cluster1 deployment19 app-crossref .hostFromPlain
  [ "$output" = "crossref.us-east-1.local" ]
}

@test "d19: gotmpl references hierarchy keys alongside template keys" {
  # globalOnly=fromGlobal from deployments/global.values.yaml; clusterdomain from template SOPS.
  run get_path cluster1 deployment19 app-crossref .mixedFromHierarchyAndSops
  [ "$output" = "fromGlobal-foo.bar" ]
}

@test "d19: gotmpl can use .Values.xxx (wrapped) syntax for SOPS key" {
  # Unified context: ATLAS adds a self-referential "Values" key so bare
  # ({{ .foo }}) and wrapped ({{ .Values.foo }}) forms both resolve.
  run get_path cluster1 deployment19 app-crossref .domainFromSopsWrapped
  [ "$output" = "app.foo.bar" ]
}

@test "d19: gotmpl can mix .Values.xxx with hierarchy + template SOPS" {
  run get_path cluster1 deployment19 app-crossref .mixedWrappedHierarchy
  [ "$output" = "fromGlobal-foo.bar" ]
}

