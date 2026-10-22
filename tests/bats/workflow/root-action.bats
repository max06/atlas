#!/usr/bin/env bats
# The scripts of the root action (action.yml): image derivation and subset
# mode (action/setup.sh), revision resolution (action/revisions.sh), the
# summary and gate (action/finish.sh). The container step itself is covered by
# review-action.bats (atlas review ci action review).

bats_require_minimum_version 1.5.0

load '../helpers/subset'

setup() {
  export GITHUB_OUTPUT="${BATS_TEST_TMPDIR}/output"
  export GITHUB_STEP_SUMMARY="${BATS_TEST_TMPDIR}/summary"
  : > "$GITHUB_OUTPUT"
  : > "$GITHUB_STEP_SUMMARY"
  ACTION="$(_repo_root)/action"
}

output_value() { sed -n "s/^$1=//p" "$GITHUB_OUTPUT" | tail -n1; }

@test "setup: a release reference maps to the image tag of the release" {
  ACTION_REF=v1.2.3 REQUESTED=off run "$ACTION/setup.sh"
  [ "$status" -eq 0 ]
  [ "$(output_value image)" = "ghcr.io/max06/atlas:v1.2.3" ]
  [ "$(output_value subset)" = "off" ]
}

@test "setup: a branch reference maps slashes to dashes" {
  ACTION_REF=feat/x REQUESTED=on run "$ACTION/setup.sh"
  [ "$(output_value image)" = "ghcr.io/max06/atlas:feat-x" ]
  [ "$(output_value subset)" = "on" ]
}

@test "setup: a commit reference maps to its sha- tag" {
  ACTION_REF=0123456789abcdef0123456789abcdef01234567 REQUESTED=shadow run "$ACTION/setup.sh"
  [ "$(output_value image)" = "ghcr.io/max06/atlas:sha-0123456" ]
}

@test "setup: the image input wins over the reference" {
  INPUT_IMAGE=example.test/atlas:dev ACTION_REF=v1.2.3 REQUESTED=off run "$ACTION/setup.sh"
  [ "$(output_value image)" = "example.test/atlas:dev" ]
}

@test "setup: no reference and no image input is an error (local path)" {
  ACTION_REF= INPUT_IMAGE= REQUESTED=off run "$ACTION/setup.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *"input 'image' is required"* ]]
}

@test "setup: the full-render label turns subsetting off; an unknown mode falls back to off" {
  ACTION_REF=main REQUESTED=on LABELED=true run "$ACTION/setup.sh"
  [ "$(output_value subset)" = "off" ]
  ACTION_REF=main REQUESTED=sometimes run "$ACTION/setup.sh"
  [ "$(output_value subset)" = "off" ]
  [[ "$output" == *"::warning::"* ]]
}

@test "revisions: a merge commit at HEAD gives merge^1 as the base" {
  local repo="${BATS_TEST_TMPDIR}/consumer"
  make_fixture_repo "$repo"
  local base; base="$(git -C "$repo" rev-parse HEAD)"
  git -C "$repo" checkout -q -b feature
  echo change > "$repo/change.txt"
  git -C "$repo" add change.txt && git -C "$repo" -c user.email=t@example.test -c user.name=t commit -q -m feature
  git -C "$repo" checkout -q -
  git -C "$repo" -c user.email=t@example.test -c user.name=t merge -q --no-ff -m merge feature
  cd "$repo"
  MERGE_CHECKOUT=success SERVER_URL=https://example.test GH_TOKEN=x REPO=o/r run "$ACTION/revisions.sh"
  [ "$status" -eq 0 ]
  [ "$(output_value base)" = "$base" ]
  [ "$(output_value head)" = "$(git -C "$repo" rev-parse HEAD)" ]
  [ "$(output_value merge_fallback)" = "false" ]
}

@test "revisions: explicit head and base are resolved as given" {
  local repo="${BATS_TEST_TMPDIR}/consumer"
  make_fixture_repo "$repo"
  local base; base="$(git -C "$repo" rev-parse HEAD)"
  echo change > "$repo/change.txt"
  git -C "$repo" add change.txt && git -C "$repo" -c user.email=t@example.test -c user.name=t commit -q -m next
  cd "$repo"
  INPUT_HEAD=HEAD INPUT_BASE=HEAD~1 SERVER_URL=https://example.test GH_TOKEN=x REPO=o/r run "$ACTION/revisions.sh"
  [ "$status" -eq 0 ]
  [ "$(output_value base)" = "$base" ]
  [ "$(output_value merge_fallback)" = "false" ]
  INPUT_HEAD=HEAD INPUT_BASE= run "$ACTION/revisions.sh"
  [ "$status" -ne 0 ]
}

@test "finish: writes the summary, fails on a render error of the merge result" {
  echo "# summary" > "${BATS_TEST_TMPDIR}/s.md"
  SUMMARY_FILE="${BATS_TEST_TMPDIR}/s.md" PR_STATUS=success REVIEW_OUTCOME=success run "$ACTION/finish.sh"
  [ "$status" -eq 0 ]
  grep -q '# summary' "$GITHUB_STEP_SUMMARY"
  SUMMARY_FILE= PR_STATUS=error REVIEW_OUTCOME=success FAIL_ON_RENDER_ERROR=true run "$ACTION/finish.sh"
  [ "$status" -ne 0 ]
  SUMMARY_FILE= PR_STATUS=error REVIEW_OUTCOME=success FAIL_ON_RENDER_ERROR=false run "$ACTION/finish.sh"
  [ "$status" -eq 0 ]
  SUMMARY_FILE= PR_STATUS=success REVIEW_OUTCOME=failure run "$ACTION/finish.sh"
  [ "$status" -ne 0 ]
}
