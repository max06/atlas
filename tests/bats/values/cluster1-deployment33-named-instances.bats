#!/usr/bin/env bats
#
# Scenario: release-name embedding for named instances.
#
# When apps[].name is set, the release name must embed the instance name
# so that multiple instances of the same template produce unique releases.
# Instance naming basics (atlas.instance.name/template) are covered by d9.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment33

setup_file() { ensure_rendered; }

# --- Release name embeds instance.name ------------------------------------

@test "d33: primary release name reflects instance.name" {
  run get_path "$CLUSTER" "$DEPLOYMENT" primary-stage3-named .probe.releaseName
  [ "$output" = "primary-stage3-named" ]
}

@test "d33: secondary release name reflects instance.name" {
  run get_path "$CLUSTER" "$DEPLOYMENT" secondary-stage3-named .probe.releaseName
  [ "$output" = "secondary-stage3-named" ]
}

