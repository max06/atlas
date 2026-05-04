#!/usr/bin/env bats
#
# Scenario: additive extraObjects list across template + deployment levels.
#
# helmfile's mergeOverwrite replaces lists wholesale rather than concatenating,
# so a deployment cannot simply set `extraObjects` at hierarchy level and
# expect its entries to be added to the template's. The working pattern is:
#
#   1. Template's values.yaml.gotmpl renders BEFORE the hierarchy overlay,
#      with the hierarchy already visible in its tpl context.
#   2. Deployment contributes entries under a DIFFERENT key
#      (`extraObjectsFromDeployment`) at hierarchy level.
#   3. Template reads that key and concats the entries into its own
#      `extraObjects` list at render time.
#   4. Hierarchy overlay doesn't touch `extraObjects` (no such key at
#      hierarchy level), so the concatenated list survives intact.
#
# Fixtures:
#   tests/templates/app-extraobjects/values.yaml.gotmpl      — template entry + concat loop
#   tests/deployments/cluster1/apps/deployment21/values.yaml — deployment entries

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment21
INSTANCE=app-extraobjects

setup_file() { ensure_rendered; }

@test "d21: extraObjects list has template entry plus deployment entries" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.extraObjects | length'
  [ "$output" = "3" ]
}

@test "d21: template entry is first in the merged list" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.extraObjects[0].metadata.name'
  [ "$output" = "from-template" ]
}

@test "d21: deployment entries appended after template entry, in order" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.extraObjects[1].metadata.name'
  [ "$output" = "from-deployment-1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.extraObjects[2].metadata.name'
  [ "$output" = "from-deployment-2" ]
}

@test "d21: entry kinds preserved across the concat (Secret vs ConfigMap)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.extraObjects[0].kind'
  [ "$output" = "Secret" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.extraObjects[1].kind'
  [ "$output" = "ConfigMap" ]
}
