#!/usr/bin/env bats
#
# Scenario: app template with inline map values in release.values that
# reference deployment-level and hierarchy-level values via Go template
# expressions (e.g. {{ .Values.instanceKey }}).
#
# ATLAS must:
#   1. Enrich the tpl context with instance inline values so expressions
#      resolve at state-build time (otherwise tpl fails with "map has no
#      entry for key").
#   2. Preserve inline map values in the emitted release.values list so
#      they reach the chart alongside the values-loader output.
#
# Precedence rules still apply: instance inline wins over the hierarchy
# (global) for overlapping keys, so {{ .Values.globalOnly }} resolves to the
# instance-provided value at tpl time AND the release gets that value.
#
# Decision (max06/atlas#86, v0.10.0): before, the hierarchy overrode the
# instance, and the three assertions on globalOnly below expected the
# hierarchy value. They were flipped on purpose.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment46
INSTANCE=app-inlinevals

setup_file() { ensure_rendered; }

@test "d46: instance-level value resolves in template inline map" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .inlineFromInstance
  [ "$output" = "fromInstanceInline" ]
}

@test "d46: an overridden hierarchy key resolves to the instance value in a template inline map" {
  # The template references {{ .Values.globalOnly }}. global.values.yaml
  # sets fromGlobal, the instance sets instanceTriesToOverride. The
  # instance is the most specific layer, so the template sees the value
  # the release gets.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .inlineFromHierarchy
  [ "$output" = "instanceTriesToOverride" ]
}

@test "d46: static inline value passes through unchanged" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .inlineStatic
  [ "$output" = "staticValue" ]
}

@test "d46: nested inline value with expression resolves" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .inlineNested.deep
  [ "$output" = "fromInstanceInline-nested" ]
}

@test "d46: instance inline key is delivered as a chart value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .instanceKey
  [ "$output" = "fromInstanceInline" ]
}

@test "d46: instance inline wins over hierarchy for globalOnly" {
  # Same key set in global.values.yaml (fromGlobal) and in the
  # deployment's instance values (instanceTriesToOverride).
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "instanceTriesToOverride" ]
}

# --- Template value FILE (.yaml.gotmpl) referencing instance values ---

@test "d46: instance-level value resolves in template values file" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .fileFromInstance
  [ "$output" = "fromInstanceInline" ]
}

@test "d46: an overridden hierarchy key resolves to the instance value in a template values file" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .fileFromHierarchy
  [ "$output" = "instanceTriesToOverride" ]
}

@test "d46: static value from template values file passes through" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .fileStatic
  [ "$output" = "fromFile" ]
}
