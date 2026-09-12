#!/usr/bin/env bats
# comment.sh inlines the render-scope fragment (scope.md) written by the
# atlas-diff entrypoint for changed-file subsetting, and stays unchanged
# without it.

load '../helpers/render'

_comment_script() { echo "$(_repo_root)/.github/actions/atlas-diff/comment.sh"; }

setup() {
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/scope.XXXXXX")"
  export COMMENT_TEMP="$TEST_TEMP"
  export GITHUB_OUTPUT="${TEST_TEMP}/github-output"
  : > "$GITHUB_OUTPUT"
  export BASELINE_STATUS=success PR_STATUS=success DIFF_STATUS=no-changes
}

body() { sed -n '/^body<<ATLAS_EOF$/,/^ATLAS_EOF$/p' "$GITHUB_OUTPUT"; }

@test "comment.sh: scope.md is inlined before the diff results" {
  printf '**Render scope:** 2 of 52 deployments (merge result), 2 of 52 (target branch) — selected from the changed paths.\n' > "$TEST_TEMP/scope.md"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body | grep -q 'Render scope:\*\* 2 of 52'
  # scope precedes the result line
  [ "$(body | grep -n 'Render scope' | cut -d: -f1)" -lt "$(body | grep -n 'No changes detected' | cut -d: -f1)" ]
}

@test "comment.sh: no scope fragment without scope.md" {
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  ! body | grep -q 'Render scope'
}

@test "comment.sh: shadow warning travels inside scope.md" {
  printf '**Render scope:** full render (changed-file subsetting runs in shadow mode).\n\n> [!WARNING]\n> **Subset shadow check FAILED:** 1 changed deployment(s) lie outside the classifier'"'"'s selection.\n' > "$TEST_TEMP/scope.md"
  export DIFF_STATUS=changes DIFF_TOTAL=1 DIFF_RELEASES=1
  echo "<details><summary>x</summary></details>" > "$TEST_TEMP/comment-diff.md"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body | grep -q 'Subset shadow check FAILED'
}
