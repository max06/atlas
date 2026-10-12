#!/usr/bin/env bats
# The one-step review action (atlas review ci action review) against the
# fixture repo: a merge commit at HEAD, the baseline as its first parent —
# the checkout the workflow hands to the atlas-review action.
#
# Uses $ATLAS_CLI when set (CI builds it once), else builds the binary with the
# local Go toolchain; without either the file is skipped.

bats_require_minimum_version 1.5.0

load '../helpers/subset'

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
  if [ -z "${ATLAS_CLI:-}" ]; then
    command -v go >/dev/null || return 0
    export ATLAS_CLI="${BATS_FILE_TMPDIR}/atlas"
    ( cd "$(_repo_root)" && go build -o "$ATLAS_CLI" ./cmd/atlas )
  fi
  export FIXTURE_REPO="${BATS_FILE_TMPDIR}/consumer"
  make_fixture_repo "$FIXTURE_REPO"
  export BASE_SHA
  BASE_SHA="$(git -C "$FIXTURE_REPO" rev-parse HEAD)"
}

setup() {
  [ -n "${ATLAS_CLI:-}" ] || skip "no ATLAS_CLI and no Go toolchain"
  git -C "$FIXTURE_REPO" checkout -q -f --detach "$BASE_SHA"
  git -C "$FIXTURE_REPO" clean -qfd
  export RUNNER_TEMP="${BATS_TEST_TMPDIR}/temp"
  export GITHUB_OUTPUT="${BATS_TEST_TMPDIR}/github-output"
  mkdir -p "$RUNNER_TEMP"
  : > "$GITHUB_OUTPUT"
}

# merge_feature <message> — commit the working tree on a feature branch and
# merge it into the base with --no-ff: HEAD = merge commit, HEAD^1 = base.
merge_feature() {
  git -C "$FIXTURE_REPO" checkout -q -B feature
  fixture_commit "$FIXTURE_REPO" "$1"
  git -C "$FIXTURE_REPO" checkout -q --detach "$BASE_SHA"
  git -C "$FIXTURE_REPO" -c user.email=atlas-tests@localhost -c user.name="atlas tests" \
    -c commit.gpgsign=false merge -q --no-ff feature -m "merge $1"
}

review() { # review <subset-mode>
  ( cd "$FIXTURE_REPO" && INPUT_BASE='HEAD^1' INPUT_SUBSET_MODE="$1" \
      INPUT_HELMFILE_PATH=helmfile.yaml.gotmpl "$ATLAS_CLI" review ci action review )
}

out() { grep "^$1=" "$GITHUB_OUTPUT" | tail -1 | cut -d= -f2-; }
body() { sed -n '/^body<<ATLAS_EOF$/,/^ATLAS_EOF$/{ /^body<<ATLAS_EOF$/d; /^ATLAS_EOF$/d; p; }' "$GITHUB_OUTPUT"; }

@test "review action: a values edit renders only its pair (subset on) and reports the release" {
  printf 'apps:\n  - template: app1\n    namespace: test\n    values:\n      - reviewMarker: changed\n' \
    > "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  merge_feature "values edit"
  run review on
  [ "$status" -eq 0 ]
  [ "$(out classify_mode)" = "subset" ]
  [ "$(out render_mode)" = "subset" ]
  [ "$(out pr_status)" = "success" ]
  [ "$(out baseline_status)" = "success" ]
  [ "$(out status)" = "changes" ]
  [ "$(out releases)" = "1" ]
  body | grep -q '^<summary>cluster1 / deployment1 / app1 (1 resources)</summary>$'
  body | grep -q '+ reviewMarker: changed'
  body | grep -q '^\*\*Render scope:\*\* 1 of '
  [ -f "$RUNNER_TEMP/summary.md" ]
  # the checkout is untouched (both sides rendered from a workspace copy)
  [ -z "$(git -C "$FIXTURE_REPO" status --porcelain)" ]
}

@test "review action: shadow mode renders everything and checks the selection" {
  printf 'apps:\n  - template: app1\n    namespace: test\n    values:\n      - reviewMarker: shadow\n' \
    > "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  merge_feature "values edit (shadow)"
  run review shadow
  [ "$status" -eq 0 ]
  [ "$(out render_mode)" = "full" ]
  [ "$(out shadow_result)" = "covered" ]
  [ "$(out releases)" = "1" ]
  body | grep -q 'changed-file subsetting runs in shadow mode'
}

@test "review action: no change in rendered manifests" {
  echo "# comment only" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  merge_feature "comment"
  run review on
  [ "$status" -eq 0 ]
  [ "$(out status)" = "no-changes" ]
  body | grep -q '^No changes detected in rendered Kubernetes manifests.$'
}

@test "review action: a merge result that does not render is reported and gated" {
  printf '{{ fail "broken on purpose" }}\n' > "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/values.yaml.gotmpl"
  merge_feature "break the merge result"
  run review on
  [ "$status" -eq 0 ]
  [ "$(out pr_status)" = "error" ]
  body | grep -q 'This PR failed to render'
  body | grep -q 'broken on purpose'
}

@test "review action: a baseline without the entry helmfile is 'missing'" {
  # baseline: the repo without the entry helmfile; the PR adds it
  git -C "$FIXTURE_REPO" checkout -q -B no-atlas "$BASE_SHA"
  cp "$FIXTURE_REPO/helmfile.yaml.gotmpl" "$BATS_TEST_TMPDIR/entry"
  git -C "$FIXTURE_REPO" rm -q helmfile.yaml.gotmpl
  fixture_commit "$FIXTURE_REPO" "no entry"
  local no_entry; no_entry="$(git -C "$FIXTURE_REPO" rev-parse HEAD)"
  git -C "$FIXTURE_REPO" checkout -q -B add-atlas
  cp "$BATS_TEST_TMPDIR/entry" "$FIXTURE_REPO/helmfile.yaml.gotmpl"
  fixture_commit "$FIXTURE_REPO" "introduce atlas"
  git -C "$FIXTURE_REPO" checkout -q --detach "$no_entry"
  git -C "$FIXTURE_REPO" -c user.email=atlas-tests@localhost -c user.name="atlas tests" \
    -c commit.gpgsign=false merge -q --no-ff add-atlas -m "merge add-atlas"
  [ -f "$FIXTURE_REPO/helmfile.yaml.gotmpl" ]
  run review on
  [ "$status" -eq 0 ]
  [ "$(out baseline_status)" = "missing" ]
  [ "$(out pr_status)" = "success" ]
  body | grep -q 'Helmfile not found on the target branch'
}

@test "review action: a baseline commit that is not there fails the step" {
  echo "# x" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  merge_feature "x"
  run --separate-stderr bash -c "cd '$FIXTURE_REPO' && INPUT_BASE=0000000000000000000000000000000000000000 '$ATLAS_CLI' review ci action review"
  [ "$status" -ne 0 ]
  [[ "$stderr" == *"baseline"* ]]
}
