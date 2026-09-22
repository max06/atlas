#!/usr/bin/env bats
# Tests for .github/actions/atlas-render/discover.sh — the map emitter with
# its support probe. The probe matters for consumers pinned to an ATLAS
# without ATLAS_DISCOVERY_MAP: their build must be recognized as "unsupported"
# (exit 2) from one cheap state parse, never by attempting the full state
# build (slow, needs SOPS).

load '../helpers/subset'

_discover() { echo "$(_actions_dir)/atlas-render/discover.sh"; }

setup() {
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/discover.XXXXXX")"
}

@test "discover.sh: writes the map for a map-aware tree (exit 0)" {
  local root
  root="$(_repo_root)"
  run bash -c "cd '$root/tests' && HELMFILE_PATH=helmfile.yaml.gotmpl bash '$(_discover)' '$TEST_TEMP/map.json'"
  [ "$status" -eq 0 ]
  [ -s "$TEST_TEMP/map.json" ]
  [ "$(jq -r .version "$TEST_TEMP/map.json")" = "1" ]
  [ "$(jq '.pairs | length' "$TEST_TEMP/map.json")" -gt 10 ]
  [[ "$output" == *"pairs →"* ]]
}

@test "discover.sh: a tree without map support is reported as unsupported (exit 2), nothing written" {
  # A plain helmfile with a real release: an ATLAS-less consumer, or an ATLAS
  # predating map mode, both build fine and carry no atlasDiscovery.
  mkdir -p "$TEST_TEMP/plain/chart/templates"
  printf 'apiVersion: v2\nname: d\nversion: 0.1.0\n' > "$TEST_TEMP/plain/chart/Chart.yaml"
  printf 'releases:\n  - name: r\n    chart: ./chart\n' > "$TEST_TEMP/plain/helmfile.yaml"
  run bash -c "cd '$TEST_TEMP/plain' && HELMFILE_PATH=helmfile.yaml bash '$(_discover)' '$TEST_TEMP/map.json'"
  [ "$status" -eq 2 ]
  [ ! -e "$TEST_TEMP/map.json" ]
  [[ "$output" == *"without ATLAS_DISCOVERY_MAP support"* ]]
}

@test "discover.sh: missing entry file is unsupported (exit 2)" {
  run bash -c "cd '$TEST_TEMP' && HELMFILE_PATH=nope.yaml.gotmpl bash '$(_discover)' '$TEST_TEMP/map.json'"
  [ "$status" -eq 2 ]
}

@test "discover.sh: a tree that does not build is an error (exit 1) with the helmfile message" {
  printf 'helmfiles:\n  - path: does-not-exist.yaml\n' > "$TEST_TEMP/broken.yaml"
  run bash -c "cd '$TEST_TEMP' && HELMFILE_PATH=broken.yaml bash '$(_discover)' '$TEST_TEMP/map.json'"
  [ "$status" -eq 1 ]
  [[ "$output" == *"helmfile build failed"* ]]
}
