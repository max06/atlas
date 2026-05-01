#!/usr/bin/env bats
#
# Scenario: stage-3 named instances. The same app template is instantiated
# twice in one deployment via apps[].name (primary, secondary). Each instance
# must:
#   - render under its own helmfile state file (per-instance fan-out from
#     stage-2) so atlas.instance.name is unambiguous
#   - produce a release whose name embeds atlas.instance.name
#   - see its own atlas.instance.name inside the values loader

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment33

setup_file() { ensure_rendered; }

# --- Both releases rendered -----------------------------------------------

@test "d33: primary instance rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" primary-stage3-named
}

@test "d33: secondary instance rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" secondary-stage3-named
}

# --- Each release sees its own instance name ------------------------------

@test "d33: primary sees instance.name=primary" {
  run get_path "$CLUSTER" "$DEPLOYMENT" primary-stage3-named .probe.instanceName
  [ "$output" = "primary" ]
}

@test "d33: secondary sees instance.name=secondary" {
  run get_path "$CLUSTER" "$DEPLOYMENT" secondary-stage3-named .probe.instanceName
  [ "$output" = "secondary" ]
}

# --- Release name embeds instance.name ------------------------------------

@test "d33: primary release name reflects instance.name" {
  run get_path "$CLUSTER" "$DEPLOYMENT" primary-stage3-named .probe.releaseName
  [ "$output" = "primary-stage3-named" ]
}

@test "d33: secondary release name reflects instance.name" {
  run get_path "$CLUSTER" "$DEPLOYMENT" secondary-stage3-named .probe.releaseName
  [ "$output" = "secondary-stage3-named" ]
}

# --- Template name is shared, instance names are not ---------------------

@test "d33: primary template is app-stage3-named" {
  run get_path "$CLUSTER" "$DEPLOYMENT" primary-stage3-named .probe.instanceTemplate
  [ "$output" = "app-stage3-named" ]
}

@test "d33: secondary template is app-stage3-named" {
  run get_path "$CLUSTER" "$DEPLOYMENT" secondary-stage3-named .probe.instanceTemplate
  [ "$output" = "app-stage3-named" ]
}
