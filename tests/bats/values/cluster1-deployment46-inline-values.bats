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
# Precedence rules still apply: hierarchy (global) wins over instance
# inline for overlapping keys. The inline map's reference to
# {{ .Values.globalOnly }} resolves to the instance-provided value at
# tpl time, but the values-loader's hierarchy overlay replaces it at
# release-evaluation time.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment46
INSTANCE=app-inlinevals

setup_file() { ensure_rendered; }

@test "d46: instance-level value resolves in template inline map" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .inlineFromInstance
  [ "$output" = "fromInstanceInline" ]
}

@test "d46: hierarchy value resolves in template inline map" {
  # The template references {{ .Values.globalOnly }}. At tpl time this
  # resolves to the hierarchy value (global.values.yaml: fromGlobal)
  # because hierarchy is merged into ctx before instance values at tpl
  # time. But the instance also sets globalOnly=instanceTriesToOverride.
  # The final rendered value depends on the tpl context merge order —
  # instance values are merged last into ctx (mergeOverwrite), so tpl
  # resolves to the instance value. However, the values-loader's
  # hierarchy FINAL PASS overrides it back to the hierarchy value.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .inlineFromHierarchy
  [ "$output" = "fromGlobal" ]
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

@test "d46: hierarchy wins over instance inline for globalOnly" {
  # Same key set in global.values.yaml (fromGlobal) and in the
  # deployment's instance values (instanceTriesToOverride). Hierarchy
  # final pass must win.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

# --- Template value FILE (.yaml.gotmpl) referencing instance values ---

@test "d46: instance-level value resolves in template values file" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .fileFromInstance
  [ "$output" = "fromInstanceInline" ]
}

@test "d46: hierarchy value resolves in template values file" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .fileFromHierarchy
  [ "$output" = "fromGlobal" ]
}

@test "d46: static value from template values file passes through" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .fileStatic
  [ "$output" = "fromFile" ]
}
