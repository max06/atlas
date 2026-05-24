#!/usr/bin/env bats
#
# Scenario: progressive accumulator across a release's values: list.
# The fixture's values: list is [inline map, plain .yaml, derived .yaml.gotmpl].
# The derived .yaml.gotmpl must see every key defined by earlier entries,
# plus hierarchy values and .Release.*.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment32
RELEASE=stage3-crossref-release

setup_file() { ensure_rendered; }

# --- Earlier entries reach the output (baseline) --------------------------

@test "d32: inline keys reach output" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .region
  [ "$output" = "us-east-1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .appName
  [ "$output" = "crossref" ]
}

@test "d32: plain .yaml keys reach output" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .tier
  [ "$output" = "backend" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .zoneSuffix
  [ "$output" = "zone-a" ]
}

# --- Progressive merge: derived gotmpl sees earlier entries ---------------

@test "d32: derived gotmpl reads earlier inline keys" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .hostFromInline
  [ "$output" = "crossref.us-east-1.local" ]
}

@test "d32: derived gotmpl reads earlier plain .yaml keys" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .hostFromPlain
  [ "$output" = "crossref.backend.zone-a" ]
}

@test "d32: derived gotmpl mixes hierarchy + earlier inline keys" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .mixedHierarchyAndInline
  [ "$output" = "fromGlobal-us-east-1" ]
}

@test "d32: derived gotmpl mixes hierarchy + earlier plain keys" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .mixedHierarchyAndPlain
  [ "$output" = "fromCluster-backend" ]
}

@test "d32: derived gotmpl reads .Release.Name alongside earlier keys" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware
  [ "$output" = "release-stage3-crossref-release-in-crossref" ]
}

@test "d32: derived gotmpl can use .Values.xxx wrapped form" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .wrappedAccess
  [ "$output" = "us-east-1/backend" ]
}
