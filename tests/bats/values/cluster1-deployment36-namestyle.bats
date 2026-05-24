#!/usr/bin/env bats
#
# Scenario: release-name munging.
#
# When apps[].name is set and differs from the template name, ATLAS rewrites
# release names so each instance produces a distinct release without forcing
# the template author to embed `{{ .Values.atlas.instance.name }}`.
#
# Default style is `<instance.name>-<release.name>` (prefix). The opt-in
# apps[].nameStyle: suffix flips to `<release.name>-<instance.name>` for
# users whose naming conventions read better that way (e.g. multi-tenant
# `vm-cust-abc` instead of `cust-abc-vm`).
#
# All three instances share the bare template release name "vm". Munging
# yields three distinct releases.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment36

setup_file() { ensure_rendered; }

# --- All three instances rendered ----------------------------------------

@test "d36: backend-vm rendered (default prefix)" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" backend-vm
}

@test "d36: frontend-vm rendered (explicit prefix)" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" frontend-vm
}

@test "d36: vm-cust-abc rendered (suffix opt-in)" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" vm-cust-abc
}

# --- Each instance sees its munged release name --------------------------

@test "d36: backend instance .Release.Name = backend-vm" {
  run get_path "$CLUSTER" "$DEPLOYMENT" backend-vm .probe.releaseName
  [ "$output" = "backend-vm" ]
}

@test "d36: frontend instance .Release.Name = frontend-vm" {
  run get_path "$CLUSTER" "$DEPLOYMENT" frontend-vm .probe.releaseName
  [ "$output" = "frontend-vm" ]
}

@test "d36: cust-abc instance .Release.Name = vm-cust-abc (suffix)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" vm-cust-abc .probe.releaseName
  [ "$output" = "vm-cust-abc" ]
}

# --- atlas.instance.name still reflects user-supplied name --------------

@test "d36: backend .Values.atlas.instance.name = backend" {
  run get_path "$CLUSTER" "$DEPLOYMENT" backend-vm .probe.instanceName
  [ "$output" = "backend" ]
}

@test "d36: cust-abc .Values.atlas.instance.name = cust-abc" {
  run get_path "$CLUSTER" "$DEPLOYMENT" vm-cust-abc .probe.instanceName
  [ "$output" = "cust-abc" ]
}
