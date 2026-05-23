#!/usr/bin/env bats
# Tests for .github/actions/atlas-diff/diff.sh
#
# These tests exercise the diff script against real ATLAS rendered output.
# The setup_file renders the full test fixture set once, then individual tests
# create modified copies to simulate PR changes.

load 'helpers/render'

_diff_script() {
  echo "$(_repo_root)/.github/actions/atlas-diff/diff.sh"
}

setup_file() {
  # Render the full test fixture set (cached across tests in this file)
  ensure_rendered

  # RENDER_DIR is set by the helper (tests/bats/helpers/render.bash)
  export BASELINE_SNAPSHOT_DIR="$RENDER_DIR"
}

setup() {
  # Each test gets its own temp directory for outputs
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/diff-test.XXXXXX")"
  export GITHUB_OUTPUT="${TEST_TEMP}/github-output"
  export DIFF_TEMP="$TEST_TEMP"
  export REPLAY_STATUS_FILE="${TEST_TEMP}/replay-status.txt"
  export SIDEDUMP_MAP_DIR="${TEST_TEMP}/sidedump-maps"
  : > "$GITHUB_OUTPUT"
  : > "$REPLAY_STATUS_FILE"
  mkdir -p "$SIDEDUMP_MAP_DIR"
}

# ── No changes ──────────────────────────────────────────────────────────────

@test "diff.sh: identical renders produce no-changes" {
  export BASELINE_DIR="$BASELINE_SNAPSHOT_DIR"
  export PR_DIR="$BASELINE_SNAPSHOT_DIR"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=no-changes' "$GITHUB_OUTPUT"
}

@test "diff.sh: empty directories produce status=empty" {
  export BASELINE_DIR="${TEST_TEMP}/empty-baseline"
  export PR_DIR="${TEST_TEMP}/empty-pr"
  mkdir -p "$BASELINE_DIR" "$PR_DIR"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=empty' "$GITHUB_OUTPUT"
}

@test "diff.sh: nonexistent directories produce status=empty" {
  export BASELINE_DIR="${TEST_TEMP}/no-such-dir"
  export PR_DIR="${TEST_TEMP}/also-no-such-dir"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=empty' "$GITHUB_OUTPUT"
}

# ── Modified release ────────────────────────────────────────────────────────

@test "diff.sh: modified release detected via dyff" {
  # Copy a single release and modify a value
  local release="cluster1/deployment1/app1"
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"
  mkdir -p "$BASELINE_DIR/$release" "$PR_DIR/$release"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$BASELINE_DIR/$release/"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$PR_DIR/$release/"

  # Simulate a value change
  find "$PR_DIR/$release" -name '*.yaml' -exec \
    sed -i 's/aChartValue: on default/aChartValue: on updated/' {} \;

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=changes' "$GITHUB_OUTPUT"
  grep -q 'releases=1' "$GITHUB_OUTPUT"

  # Check output files exist
  [ -f "${DIFF_TEMP}/comment-diff.md" ]
  [ -f "${DIFF_TEMP}/summary-diff.md" ]
  [ -f "${DIFF_TEMP}/affected.md" ]

  # Comment should mention the release
  grep -q 'deployment1' "${DIFF_TEMP}/comment-diff.md"
}

# ── Added release ───────────────────────────────────────────────────────────

@test "diff.sh: added release (PR-only) produces changes" {
  local release="cluster1/deployment1/app1"
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"
  mkdir -p "$BASELINE_DIR" "$PR_DIR/$release"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$PR_DIR/$release/"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=changes' "$GITHUB_OUTPUT"

  # dyff should show the full document as added
  grep -q 'document added' "${DIFF_TEMP}/comment-diff.md" || \
    grep -q 'deployment1' "${DIFF_TEMP}/comment-diff.md"
}

# ── Removed release ─────────────────────────────────────────────────────────

@test "diff.sh: removed release (baseline-only) is suppressed" {
  local release="cluster1/deployment1/app1"
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"
  mkdir -p "$BASELINE_DIR/$release" "$PR_DIR"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$BASELINE_DIR/$release/"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=changes' "$GITHUB_OUTPUT"
  grep -q 'suppressed_removed=1' "$GITHUB_OUTPUT"

  # Should be marked as removed in the comment
  grep -q 'removed' "${DIFF_TEMP}/comment-diff.md"
}

# ── Redaction map bucketing ─────────────────────────────────────────────────

@test "diff.sh: release with no-map in replay status is suppressed" {
  local release="cluster1/deployment1/app1"
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"
  mkdir -p "$BASELINE_DIR/$release" "$PR_DIR/$release"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$BASELINE_DIR/$release/"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$PR_DIR/$release/"

  # Modify PR side so it's not byte-identical
  find "$PR_DIR/$release" -name '*.yaml' -exec \
    sed -i 's/aChartValue: on default/aChartValue: changed/' {} \;

  # Mark this release as no-map in the replay status
  echo "no-map $release" > "$REPLAY_STATUS_FILE"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'suppressed_nomap=1' "$GITHUB_OUTPUT"
}

@test "diff.sh: release with no-map but byte-identical is skipped silently" {
  local release="cluster1/deployment1/app1"
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"
  mkdir -p "$BASELINE_DIR/$release" "$PR_DIR/$release"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$BASELINE_DIR/$release/"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$PR_DIR/$release/"

  # Mark as no-map but content is identical
  echo "no-map $release" > "$REPLAY_STATUS_FILE"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  # Should produce no-changes (the only release is identical, skipped)
  grep -q 'status=no-changes' "$GITHUB_OUTPUT"
}

@test "diff.sh: empty sidedump map file means no secrets — show full diff" {
  local release="cluster1/deployment1/app1"
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"
  mkdir -p "$BASELINE_DIR/$release" "$PR_DIR/$release"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$BASELINE_DIR/$release/"
  cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$PR_DIR/$release/"

  # Modify PR side
  find "$PR_DIR/$release" -name '*.yaml' -exec \
    sed -i 's/aChartValue: on default/aChartValue: changed/' {} \;

  # Mark as no-map in replay status, BUT create an empty map file
  echo "no-map $release" > "$REPLAY_STATUS_FILE"
  mkdir -p "$SIDEDUMP_MAP_DIR/$release"
  # Trim trailing release name — the map file is at the release level
  local map_path="$SIDEDUMP_MAP_DIR/${release}.json"
  mkdir -p "$(dirname "$map_path")"
  echo '{}' > "$map_path"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]

  # Should NOT be suppressed — empty map means no secrets
  grep -q 'status=changes' "$GITHUB_OUTPUT"
  # suppressed_nomap should be 0 (the empty-map release is treated as safe)
  grep -q 'suppressed_nomap=0' "$GITHUB_OUTPUT" || \
    ! grep -q 'suppressed_nomap' "$GITHUB_OUTPUT"
}

# ── Multiple releases ───────────────────────────────────────────────────────

@test "diff.sh: multiple changed releases counted correctly" {
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"

  # Copy two releases
  for rel in cluster1/deployment1/app1 cluster1/deployment3/app-novals; do
    mkdir -p "$BASELINE_DIR/$rel" "$PR_DIR/$rel"
    cp -r "$BASELINE_SNAPSHOT_DIR/$rel/." "$BASELINE_DIR/$rel/"
    cp -r "$BASELINE_SNAPSHOT_DIR/$rel/." "$PR_DIR/$rel/"
  done

  # Modify both
  find "$PR_DIR" -name '*.yaml' -exec \
    sed -i 's/chart1-0.1.0/chart1-0.2.0/' {} \;

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=changes' "$GITHUB_OUTPUT"
  grep -q 'releases=2' "$GITHUB_OUTPUT"
}

# ── Truncation ──────────────────────────────────────────────────────────────

@test "diff.sh: large diffs trigger truncation flag" {
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"

  # Create a release with a very large manifest
  local release="cluster1/deployment1/app1"
  mkdir -p "$BASELINE_DIR/$release/chart1/templates"
  mkdir -p "$PR_DIR/$release/chart1/templates"

  # Generate a large YAML file (>55KB)
  {
    echo "---"
    echo "apiVersion: v1"
    echo "kind: ConfigMap"
    echo "metadata:"
    echo "  name: big-config"
    echo "data:"
    for i in $(seq 1 2000); do
      echo "  key${i}: baseline-value-${i}"
    done
  } > "$BASELINE_DIR/$release/chart1/templates/configmap.yaml"

  {
    echo "---"
    echo "apiVersion: v1"
    echo "kind: ConfigMap"
    echo "metadata:"
    echo "  name: big-config"
    echo "data:"
    for i in $(seq 1 2000); do
      echo "  key${i}: updated-value-${i}"
    done
  } > "$PR_DIR/$release/chart1/templates/configmap.yaml"

  run bash "$(_diff_script)"
  [ "$status" -eq 0 ]
  grep -q 'status=changes' "$GITHUB_OUTPUT"
  grep -q 'truncated=true' "$GITHUB_OUTPUT"
}

# ── Grouped cluster paths ──────────────────────────────────────────────────

@test "diff.sh: grouped cluster path (group1/cluster2) handled correctly" {
  local release="group1/cluster2/deployment2/app1"
  export BASELINE_DIR="${TEST_TEMP}/baseline"
  export PR_DIR="${TEST_TEMP}/pr"

  # Only use this release if it exists in the fixture set
  if [ -d "$BASELINE_SNAPSHOT_DIR/$release" ]; then
    mkdir -p "$BASELINE_DIR/$release" "$PR_DIR/$release"
    cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$BASELINE_DIR/$release/"
    cp -r "$BASELINE_SNAPSHOT_DIR/$release/." "$PR_DIR/$release/"

    find "$PR_DIR/$release" -name '*.yaml' -exec \
      sed -i 's/chart1-0.1.0/chart1-0.2.0/' {} \;

    run bash "$(_diff_script)"
    [ "$status" -eq 0 ]
    grep -q 'status=changes' "$GITHUB_OUTPUT"
    # The release header should contain the grouped path
    grep -q 'group1/cluster2' "${DIFF_TEMP}/comment-diff.md"
  else
    skip "group1/cluster2/deployment2/app1 not in fixture set"
  fi
}
