#!/usr/bin/env bats
# Equivalence oracle for changed-file render subsetting.
#
# For a set of PR scenarios against the fixture repo, run the review pipeline
# twice — full render of both revisions vs. subset render of the classifier's
# selection — and require the diff output to be byte-identical. This is the
# property the cutover from shadow mode rests on: the subset shows exactly the
# changes the full render shows, no more, no less.
#
# Also exercises shadow-check.sh: the full diff's affected releases must lie
# inside the classifier's selection.
#
# Cost: one full render of the base revision (shared), one full render of each
# scenario's head revision, plus the (cheap) subset renders.
#
# Both revisions are rendered from the SAME directory ($WORKSPACE, re-populated
# per revision) — like actions/checkout does in the workflow. Rendered output
# embeds atlas.cwd and absolute deployment paths (the fixture chart serializes
# all values), so two revisions at different paths would differ everywhere.

load '../helpers/subset'

_render_script() { echo "$(_actions_dir)/atlas-render/render.sh"; }
_diff_script()   { echo "$(_actions_dir)/atlas-diff/diff.sh"; }
_shadow_script() { echo "$(_actions_dir)/atlas-diff/shadow-check.sh"; }

_setup_helm_plugins() {
  local default_plugins
  default_plugins="$(helm env HELM_PLUGINS 2>/dev/null || echo "")"
  export HELM_PLUGINS="${default_plugins:+${default_plugins}:}$(_repo_root)/.github/actions/atlas-render"
}

# render_side <tree> <label> <out-dir> [pairs-file]
# Runs render.sh in <tree> (full mode, or subset mode with the pairs file).
render_side() {
  local tree="$1" label="$2" out="$3" pairs="${4:-}"
  mkdir -p "$out"
  ( cd "$tree" && \
    HELMFILE_PATH="$tree/helmfile.yaml.gotmpl" SNAPSHOT_LABEL="$label" RENDER_TEMP="$out" \
    BLOCKING=true GITHUB_OUTPUT="$out/$label-output" \
    env ${pairs:+RENDER_PAIRS_FILE="$pairs"} \
    bash "$(_render_script)" > "$out/$label-render.log" 2>&1 )
  grep -q '^status=success' "$out/$label-output"
}

# diff_sides <baseline-snapshot> <pr-snapshot> <out-dir>
diff_sides() {
  mkdir -p "$3"
  BASELINE_DIR="$1" PR_DIR="$2" DIFF_TEMP="$3" GITHUB_OUTPUT="$3/output" \
  REPLAY_STATUS_FILE="$3/replay-status.txt" SIDEDUMP_MAP_DIR="$3/no-maps" \
    bash -c ': > "$REPLAY_STATUS_FILE"; mkdir -p "$SIDEDUMP_MAP_DIR"; bash "'"$(_diff_script)"'"' > "$3/diff.log" 2>&1
  touch "$3/comment-diff.md" "$3/affected-paths.txt"
}

# checkout_into <rev> — populate $WORKSPACE with that revision's tree.
checkout_into() {
  rm -rf "$WORKSPACE"
  export_rev "$FIXTURE_REPO" "$1" "$WORKSPACE"
}

setup_file() {
  _setup_helm_plugins
  export FIXTURE_REPO="${BATS_FILE_TMPDIR}/consumer"
  make_fixture_repo "$FIXTURE_REPO"
  export BASE_SHA
  BASE_SHA="$(git -C "$FIXTURE_REPO" rev-parse HEAD)"
  export WORKSPACE="${BATS_FILE_TMPDIR}/workspace"
  # base revision full render, shared by all scenarios
  export BASE_FULL="${BATS_FILE_TMPDIR}/base-full"
  checkout_into "$BASE_SHA"
  render_side "$WORKSPACE" baseline "$BASE_FULL"
}

setup() {
  git -C "$FIXTURE_REPO" checkout -q --detach "$BASE_SHA"
  git -C "$FIXTURE_REPO" clean -qfd
  OUT="$(mktemp -d "${BATS_FILE_TMPDIR}/equiv.XXXXXX")"
}

# run_equivalence <message>
# Commits the working tree, classifies, renders full + subset, diffs both,
# and asserts identical diff output plus shadow coverage.
run_equivalence() {
  fixture_commit "$FIXTURE_REPO" "$1"
  classify_revisions "$FIXTURE_REPO" "$BASE_SHA" HEAD "$OUT/classify"
  [ "$(gh_output "$OUT/classify" mode)" = "subset" ]

  # head revision: full render (oracle) and subset render, same workspace path
  checkout_into HEAD
  render_side "$WORKSPACE" pr "$OUT/head-full"
  render_side "$WORKSPACE" pr "$OUT/head-subset" "$OUT/classify/pairs-pr.txt"
  grep -q '^render_mode=subset' "$OUT/head-subset/pr-output"
  # base revision: subset render (its full render is the shared BASE_FULL)
  checkout_into "$BASE_SHA"
  render_side "$WORKSPACE" baseline "$OUT/base-subset" "$OUT/classify/pairs-baseline.txt"

  diff_sides "$BASE_FULL/baseline-snapshot" "$OUT/head-full/pr-snapshot" "$OUT/diff-full"
  diff_sides "$OUT/base-subset/baseline-snapshot" "$OUT/head-subset/pr-snapshot" "$OUT/diff-subset"

  # the oracle
  cmp -s "$OUT/diff-full/comment-diff.md" "$OUT/diff-subset/comment-diff.md" \
    || { echo "comment-diff.md differs between full and subset:"; diff "$OUT/diff-full/comment-diff.md" "$OUT/diff-subset/comment-diff.md" | head -40; return 1; }
  cmp -s <(sort "$OUT/diff-full/affected-paths.txt") <(sort "$OUT/diff-subset/affected-paths.txt")
  [ "$(grep '^status=' "$OUT/diff-full/output")" = "$(grep '^status=' "$OUT/diff-subset/output")" ]

  # shadow check on the full diff
  AFFECTED_PATHS_FILE="$OUT/diff-full/affected-paths.txt" CLASSIFY_JSON="$OUT/classify/classify.json" \
    GITHUB_OUTPUT="$OUT/shadow-output" bash "$(_shadow_script)" > "$OUT/shadow.log" 2>&1
  [ "$(grep '^shadow_result=' "$OUT/shadow-output" | cut -d= -f2)" = "covered" ]
}

@test "equivalence: cluster-level deployment value change" {
  printf 'apps:\n  - template: app1\n    namespace: test\n    values:\n      - equivalenceMarker: changed\n' \
    > "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  run_equivalence "deployment1 values"
  grep -q 'status=changes' "$OUT/diff-full/output"
  grep -q '^cluster1/deployment1/' "$OUT/diff-full/affected-paths.txt"
}

@test "equivalence: template change affecting several deployments" {
  # app-novals-b's single release has no values block; append one so the
  # rendered ConfigMap (chart1 serializes .Values) actually changes.
  printf '    values:\n      - equivalenceMarker: template-changed\n' >> "$FIXTURE_REPO/templates/app-novals-b/helmfile.yaml.gotmpl"
  run_equivalence "template app-novals-b"
  grep -q 'status=changes' "$OUT/diff-full/output"
  grep -q '^cluster1/deployment7/app-novals-b$' "$OUT/diff-full/affected-paths.txt"
}

@test "equivalence: group values change" {
  echo "equivalenceMarker: group-changed" >> "$FIXTURE_REPO/deployments/group1/group.values.yaml"
  run_equivalence "group1 values"
  grep -q 'status=changes' "$OUT/diff-full/output"
}

@test "equivalence: cluster values change" {
  echo "equivalenceMarker: cluster-changed" >> "$FIXTURE_REPO/deployments/cluster1/cluster.values.yaml"
  run_equivalence "cluster1 values"
  grep -q 'status=changes' "$OUT/diff-full/output"
}

@test "equivalence: cluster-level deployment shadowing a group one" {
  mkdir -p "$FIXTURE_REPO/deployments/group1/cluster2/apps/deployment6"
  printf 'apps:\n  - template: app-group\n    namespace: test\n    values:\n      - equivalenceMarker: shadowed\n' \
    > "$FIXTURE_REPO/deployments/group1/cluster2/apps/deployment6/deployment.yaml"
  run_equivalence "shadow deployment6"
  grep -q 'status=changes' "$OUT/diff-full/output"
}

@test "equivalence: removed deployment" {
  git -C "$FIXTURE_REPO" rm -rq deployments/cluster1/apps/deployment3
  run_equivalence "remove deployment3"
  grep -q 'status=changes' "$OUT/diff-full/output"
  grep -q '^cluster1/deployment3/' "$OUT/diff-full/affected-paths.txt"
}

@test "equivalence: new cluster inheriting group and global deployments" {
  mkdir -p "$FIXTURE_REPO/deployments/group1/cluster4/apps/newapp"
  printf 'apps:\n  - template: app-novals\n    namespace: test\n' > "$FIXTURE_REPO/deployments/group1/cluster4/apps/newapp/deployment.yaml"
  cp "$FIXTURE_REPO/deployments/group1/cluster2/cluster.values.yaml" "$FIXTURE_REPO/deployments/group1/cluster4/cluster.values.yaml"
  run_equivalence "add cluster4"
  grep -q 'status=changes' "$OUT/diff-full/output"
  grep -q '^group1/cluster4/deployment6/' "$OUT/diff-full/affected-paths.txt"
}

@test "equivalence: no-op change produces no-changes in both modes" {
  echo "# comment only" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  run_equivalence "comment only"
  grep -q 'status=no-changes' "$OUT/diff-subset/output"
}

@test "shadow-check: reports a release outside the selection" {
  echo "# touched" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  fixture_commit "$FIXTURE_REPO" "shadow negative"
  classify_revisions "$FIXTURE_REPO" "$BASE_SHA" HEAD "$OUT/classify"
  printf 'cluster1/deployment1/app1\ngroup1/cluster2/deployment2/app1\n' > "$OUT/affected.txt"
  run env AFFECTED_PATHS_FILE="$OUT/affected.txt" CLASSIFY_JSON="$OUT/classify/classify.json" \
    GITHUB_OUTPUT="$OUT/shadow-output" bash "$(_shadow_script)"
  [ "$status" -eq 0 ]
  grep -q '^shadow_result=uncovered' "$OUT/shadow-output"
  grep -q '^shadow_missed=1' "$OUT/shadow-output"
  grep -q 'group1/cluster2.*deployment2' "$OUT/classify/shadow-check.md"
}
