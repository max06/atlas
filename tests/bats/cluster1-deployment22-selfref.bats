#!/usr/bin/env bats
#
# Scenario: self-reference pass over merged hierarchy values. A hierarchy-level
# values.yaml.gotmpl can now reference keys defined EARLIER in the same file via
# {{ .Values.key }}. The first-pass render (inside the hierarchy loader) emits
# the template expression as a literal string via backtick escape; a post-merge
# tpl pass then resolves those literals against the fully merged tree.
#
# Fixture:
#   tests/deployments/cluster1/apps/deployment22/
#     deployment.yaml        uses app-novals (pass-through chart1)
#     values.yaml.gotmpl     domain / clusterDomain / serviceHost — 2-hop chain
#
# Chained references (serviceHost -> clusterDomain -> domain) exercise the
# fixed-point iteration of the self-ref pass.

load 'helpers/render'

setup_file() {
  ensure_rendered
  ensure_rendered_redacted
}

@test "d22: plain key passes through unchanged" {
  run get_path cluster1 deployment22 app-novals .domain
  [ "$output" = "foo.bar" ]
}

@test "d22: self-reference resolves to value from same file" {
  run get_path cluster1 deployment22 app-novals .clusterDomain
  [ "$output" = "cluster1.foo.bar" ]
}

@test "d22: chained self-reference resolves through multiple passes" {
  run get_path cluster1 deployment22 app-novals .serviceHost
  [ "$output" = "api.cluster1.foo.bar" ]
}

# --- Redacted render: non-SOPS self-ref values pass through unchanged -------
#
# Plain-value self-references are not SOPS-derived, so the redacted tree
# resolves them identically to the real tree → the diff emits no pair → the
# post-renderer leaves them alone. This locks in that the self-ref pass does
# not create spurious redaction pairs for plain values.

@test "redact d22: plain self-ref clusterDomain preserved" {
  run get_path_redacted cluster1 deployment22 app-novals .clusterDomain
  [ "$output" = "cluster1.foo.bar" ]
}

@test "redact d22: plain chained self-ref serviceHost preserved" {
  run get_path_redacted cluster1 deployment22 app-novals .serviceHost
  [ "$output" = "api.cluster1.foo.bar" ]
}
