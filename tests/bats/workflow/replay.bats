#!/usr/bin/env bats
# replay.sh: replays the PR side's redaction maps over the baseline render.
# Covers the edge cases around missing inputs (the scrub itself is tested
# through scrub-baseline.sh in tests/bats/redaction/).

bats_require_minimum_version 1.5.0

load '../helpers/subset'

setup() {
  REPLAY="$(_actions_dir)/atlas-diff/replay.sh"
  export SCRUB_SCRIPT="$(_actions_dir)/atlas-diff/scrub-baseline.sh"
  export REPLAY_TEMP="$BATS_TEST_TMPDIR/temp"
  export GITHUB_OUTPUT="$BATS_TEST_TMPDIR/github-output"
  mkdir -p "$REPLAY_TEMP"
  : > "$GITHUB_OUTPUT"
  export BASELINE_DIR="$BATS_TEST_TMPDIR/baseline"
  mkdir -p "$BASELINE_DIR/cluster1/deployment1/app1/chart1/templates"
  printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: v\n' \
    > "$BASELINE_DIR/cluster1/deployment1/app1/chart1/templates/cm.yaml"
}

@test "replay: no baseline output is skipped" {
  BASELINE_DIR="$BATS_TEST_TMPDIR/none" SIDEDUMP_MAP_DIR="" run "$REPLAY"
  [ "$status" -eq 0 ]
  grep -q '^no_map_count=0$' "$GITHUB_OUTPUT"
}

@test "replay: an empty map directory input (PR side failed early) does not abort" {
  SIDEDUMP_MAP_DIR="" run "$REPLAY"
  [ "$status" -eq 0 ]
  [[ "$output" == *"No PR-side redaction maps captured"* ]]
  grep -q '^no_map_count=1$' "$GITHUB_OUTPUT"
}

@test "replay: a map directory that does not exist counts every release as no-map" {
  SIDEDUMP_MAP_DIR="$BATS_TEST_TMPDIR/maps-missing" run "$REPLAY"
  [ "$status" -eq 0 ]
  grep -q '^no_map_count=1$' "$GITHUB_OUTPUT"
}
