#!/usr/bin/env bats
# Tests for .github/actions/atlas-diff/comment.sh
#
# Exercises the comment builder with various combinations of render status,
# diff status, version pins, and security flags.

load 'helpers/render'

_comment_script() {
  echo "$(_repo_root)/.github/actions/atlas-diff/comment.sh"
}

setup() {
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/comment-test.XXXXXX")"
  export GITHUB_OUTPUT="${TEST_TEMP}/github-output"
  export COMMENT_TEMP="$TEST_TEMP"
  : > "$GITHUB_OUTPUT"

  # Defaults — tests override what they need
  export BASELINE_STATUS="success"
  export PR_STATUS="success"
  export DIFF_STATUS="no-changes"
  export MERGE_FALLBACK=""
  export BASELINE_FILTER="true"
  export PR_FILTER="true"
  export WORKFLOW_PIN_TARGET=""
  export WORKFLOW_PIN_MERGE=""
  export LATEST_ATLAS_TAG=""
  export DIFF_TOTAL="0"
  export DIFF_RELEASES="0"
  export DIFF_TRUNCATED=""
  export HELMFILE_PATH="helmfile.yaml.gotmpl"
  export RUN_URL=""
  export BASELINE_STDERR_LOG="${TEST_TEMP}/baseline-stderr.log"
  export PR_STDERR_LOG="${TEST_TEMP}/pr-stderr.log"
  : > "$BASELINE_STDERR_LOG"
  : > "$PR_STDERR_LOG"
}

# Helper: extract body from GITHUB_OUTPUT (between ATLAS_EOF delimiters)
get_body() {
  sed -n '/^body<<ATLAS_EOF$/,/^ATLAS_EOF$/{ /^body<<ATLAS_EOF$/d; /^ATLAS_EOF$/d; p; }' "$GITHUB_OUTPUT"
}

# ── Basic output ────────────────────────────────────────────────────────────

@test "comment.sh: produces ATLAS Review header" {
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"## ATLAS Review"* ]]
}

@test "comment.sh: no-changes produces clean message" {
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"No changes detected"* ]]
}

@test "comment.sh: includes ATLAS link in footer" {
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"github.com/max06/atlas"* ]]
}

# ── PR render error ─────────────────────────────────────────────────────────

@test "comment.sh: PR render error shows CAUTION banner" {
  export PR_STATUS="error"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"[!CAUTION]"* ]]
  [[ "$body" == *"failed to render"* ]]
}

@test "comment.sh: PR render error includes helmfile command" {
  export PR_STATUS="error"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"helmfile -f helmfile.yaml.gotmpl template"* ]]
}

@test "comment.sh: PR render error includes stderr log" {
  export PR_STATUS="error"
  echo "some error output" > "$PR_STDERR_LOG"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"some error output"* ]]
}

# ── Baseline issues ─────────────────────────────────────────────────────────

@test "comment.sh: baseline error shows WARNING (non-blocking)" {
  export BASELINE_STATUS="error"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"[!WARNING]"* ]]
  [[ "$body" == *"Target branch failed to render"* ]]
}

@test "comment.sh: baseline missing shows NOTE" {
  export BASELINE_STATUS="missing"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"[!NOTE]"* ]]
  [[ "$body" == *"Helmfile not found"* ]]
}

# ── Merge fallback ──────────────────────────────────────────────────────────

@test "comment.sh: merge fallback shows WARNING" {
  export MERGE_FALLBACK="true"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"[!WARNING]"* ]]
  [[ "$body" == *"Merge ref unavailable"* ]]
}

# ── Workflow version checks ─────────────────────────────────────────────────

@test "comment.sh: pinned to old tag shows update notice" {
  export WORKFLOW_PIN_MERGE="v0.2.0"
  export LATEST_ATLAS_TAG="v0.3.0"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"@v0.2.0"* ]]
  [[ "$body" == *"@v0.3.0"* ]]
  [[ "$body" == *"security fixes"* ]]
}

@test "comment.sh: pinned to main is silent" {
  export WORKFLOW_PIN_MERGE="main"
  export LATEST_ATLAS_TAG="v0.3.0"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  # Should NOT mention version pinning
  [[ "$body" != *"security fixes"* ]]
}

@test "comment.sh: pin change detected" {
  export WORKFLOW_PIN_TARGET="v0.2.0"
  export WORKFLOW_PIN_MERGE="v0.3.0"
  export LATEST_ATLAS_TAG="v0.3.0"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"changes the ATLAS workflow pin"* ]]
}

@test "comment.sh: pinned to latest is silent" {
  export WORKFLOW_PIN_MERGE="v0.3.0"
  export LATEST_ATLAS_TAG="v0.3.0"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" != *"security fixes"* ]]
  [[ "$body" != *"changes the ATLAS workflow pin"* ]]
}

# ── Security messaging ──────────────────────────────────────────────────────

@test "comment.sh: both sides old shows CAUTION about secret leaks" {
  export BASELINE_FILTER="false"
  export PR_FILTER="false"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"[!CAUTION]"* ]]
  [[ "$body" == *"Both sides"* ]]
  [[ "$body" == *"leaks template-level secrets"* ]]
}

@test "comment.sh: baseline old only shows baseline-side warning" {
  export BASELINE_FILTER="false"
  export PR_FILTER="true"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"target branch uses an older ATLAS"* ]]
}

@test "comment.sh: PR old only shows PR-side warning" {
  export BASELINE_FILTER="true"
  export PR_FILTER="false"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"This PR pins an older ATLAS"* ]]
}

@test "comment.sh: both sides current — no security warning" {
  export BASELINE_FILTER="true"
  export PR_FILTER="true"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" != *"leaks template-level secrets"* ]]
}

# ── Diff results ────────────────────────────────────────────────────────────

@test "comment.sh: changes status includes diff content" {
  export DIFF_STATUS="changes"
  export DIFF_TOTAL="3"
  export DIFF_RELEASES="2"
  echo "some diff content here" > "${COMMENT_TEMP}/comment-diff.md"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"Changes detected in **3** release(s)"* ]]
  [[ "$body" == *"Affected releases"* ]]
  [[ "$body" == *"some diff content here"* ]]
}

@test "comment.sh: truncated flag shows note with job summary link" {
  export DIFF_STATUS="changes"
  export DIFF_TOTAL="1"
  export DIFF_RELEASES="1"
  export DIFF_TRUNCATED="true"
  export RUN_URL="https://github.com/foo/bar/actions/runs/123"
  echo "truncated diff" > "${COMMENT_TEMP}/comment-diff.md"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"truncated"* ]]
  [[ "$body" == *"job summary"* ]]
}

@test "comment.sh: empty status shows configuration warning" {
  export DIFF_STATUS="empty"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"No releases were rendered"* ]]
}

@test "comment.sh: both sides error shows unable to diff" {
  export PR_STATUS="error"
  export BASELINE_STATUS="error"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  [[ "$body" == *"Unable to generate a diff"* ]]
}

# ── Job summary ─────────────────────────────────────────────────────────────

@test "comment.sh: writes job summary file" {
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  [ -f "${COMMENT_TEMP}/summary.md" ]
  grep -q 'ATLAS Review Summary' "${COMMENT_TEMP}/summary.md"
}

@test "comment.sh: job summary includes pin detection table" {
  export WORKFLOW_PIN_TARGET="v0.2.0"
  export WORKFLOW_PIN_MERGE="v0.3.0"
  export LATEST_ATLAS_TAG="v0.3.0"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  grep -q 'Workflow pin detection' "${COMMENT_TEMP}/summary.md"
  grep -q 'v0.2.0' "${COMMENT_TEMP}/summary.md"
  grep -q 'v0.3.0' "${COMMENT_TEMP}/summary.md"
}

# ── Size constraint ─────────────────────────────────────────────────────────

@test "comment.sh: body stays under 65KB" {
  export DIFF_STATUS="changes"
  export DIFF_TOTAL="1"
  export DIFF_RELEASES="1"
  # Create a large but not huge diff file
  python3 -c "print('x' * 50000)" > "${COMMENT_TEMP}/comment-diff.md"
  run bash "$(_comment_script)"
  [ "$status" -eq 0 ]
  body=$(get_body)
  body_size=${#body}
  [ "$body_size" -lt 65000 ]
}
