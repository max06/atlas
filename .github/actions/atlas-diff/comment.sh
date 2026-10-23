#!/usr/bin/env bash
# ATLAS snapshot-review: build the PR comment and job summary markdown.
#
# Assembles all the pieces: version checks, security warnings, error reports,
# and diff content into a single markdown body for the PR comment and a separate
# (potentially larger) body for the job summary.
#
# Required environment:
#   BASELINE_STATUS   — success / error / missing
#   PR_STATUS         — success / error
#   DIFF_STATUS       — empty / no-changes / changes
#   COMMENT_TEMP      — temporary directory containing diff output files
#
# Optional environment:
#   MERGE_FALLBACK    — "true" if merge ref was unavailable
#   BASELINE_FILTER   — "true"/"false" (selector probe result for baseline)
#   PR_FILTER         — "true"/"false" (selector probe result for PR)
#   WORKFLOW_PIN_TARGET — ATLAS workflow ref detected on target branch
#   WORKFLOW_PIN_MERGE  — ATLAS workflow ref detected on merge result
#   LATEST_ATLAS_TAG    — latest ATLAS release tag
#   DIFF_TOTAL          — number of changed resources
#   DIFF_RELEASES       — number of affected releases
#   DIFF_TRUNCATED      — "true" if diffs were truncated
#   HELMFILE_PATH       — helmfile entry point path (for error message)
#   RUN_URL             — GitHub Actions run URL (for links)
#   BASELINE_STDERR_LOG — path to baseline stderr log
#   PR_STDERR_LOG       — path to PR stderr log
#   GITHUB_OUTPUT       — output file for step outputs (default: /dev/null)
#
# Outputs (written to $GITHUB_OUTPUT):
#   body — the full PR comment markdown (using heredoc delimiter)
#
# Output files:
#   $COMMENT_TEMP/summary.md — job summary markdown
#
# Optional input files (inlined when present):
#   $COMMENT_TEMP/scope.md   — render-scope fragment (changed-file subsetting)

set -euo pipefail

BASELINE_STATUS="${BASELINE_STATUS:?}"
PR_STATUS="${PR_STATUS:?}"
DIFF_STATUS="${DIFF_STATUS:?}"
COMMENT_TEMP="${COMMENT_TEMP:-${RUNNER_TEMP:-/tmp}}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"

MERGE_FALLBACK="${MERGE_FALLBACK:-}"
BASELINE_FILTER="${BASELINE_FILTER:-true}"
PR_FILTER="${PR_FILTER:-true}"
WORKFLOW_PIN_TARGET="${WORKFLOW_PIN_TARGET:-}"
WORKFLOW_PIN_MERGE="${WORKFLOW_PIN_MERGE:-}"
LATEST_ATLAS_TAG="${LATEST_ATLAS_TAG:-}"
LATEST_MAIN_SHA="${LATEST_MAIN_SHA:-}"
DIFF_TOTAL="${DIFF_TOTAL:-0}"
DIFF_RELEASES="${DIFF_RELEASES:-0}"
DIFF_TRUNCATED="${DIFF_TRUNCATED:-}"
HELMFILE_PATH="${HELMFILE_PATH:-helmfile.yaml.gotmpl}"
RUN_URL="${RUN_URL:-}"
BASELINE_STDERR_LOG="${BASELINE_STDERR_LOG:-${COMMENT_TEMP}/baseline-stderr.log}"
PR_STDERR_LOG="${PR_STDERR_LOG:-${COMMENT_TEMP}/pr-stderr.log}"

# Read error logs (truncated to keep comment reasonable)
BASELINE_ERRORS=""
PR_ERRORS=""
# error_excerpt <log> — the lines that explain a failure: error lines without
# progress output, consecutive duplicates folded with a count (a parallel
# render repeats one error per pair); the last 50 lines when none matches.
error_excerpt() {
  local lines
  lines=$(grep -vE '^(wrote |Templating release=|Adding repo |"?[A-Za-z0-9._-]+"? has been added to your repositories|Building dependency|Pulling |Comparing |Listing |Hang tight|Update Complete|Successfully |::debug::)' "$1" \
    | grep -iE '(^|[^a-z])(error|fail|fatal|panic|exception|not found|forbidden|denied|timed? ?out|too long|invalid|cannot|could not)' \
    | uniq -c | sed -E 's/^ *1 (.*)$/\1/; s/^ *([0-9]+) (.*)$/\2  (×\1)/' | head -50)
  if [ -n "$lines" ]; then printf '%s' "$lines"; else tail -50 "$1"; fi
}
[ -f "$BASELINE_STDERR_LOG" ] && [ -s "$BASELINE_STDERR_LOG" ] && \
  BASELINE_ERRORS=$(error_excerpt "$BASELINE_STDERR_LOG")
[ -f "$PR_STDERR_LOG" ] && [ -s "$PR_STDERR_LOG" ] && \
  PR_ERRORS=$(error_excerpt "$PR_STDERR_LOG")

# Build the comment body
BODY=""
emit() { BODY="${BODY}${1}"$'\n'; }

emit '## ATLAS Review'
emit ''

# ── Merge-result render error (blocking) ──
if [ "$PR_STATUS" = "error" ]; then
  emit '> [!CAUTION]'
  emit '> **This PR failed to render.** The pipeline will fail — fix the errors below before merging.'
  emit '>'
  emit '> To diagnose locally, run:'
  emit '>'
  emit '> ```bash'
  emit "> helmfile -f ${HELMFILE_PATH} template --skip-schema-validation"
  emit '> ```'
  emit '>'
  emit '> If the local render succeeds but CI still fails, please file an issue at <https://github.com/max06/atlas/issues>.'
  emit ''
  if [ -n "$PR_ERRORS" ]; then
    emit '<details>'
    emit '<summary>Render errors (merge result)</summary>'
    emit ''
    emit '```'
    emit "$PR_ERRORS"
    emit '```'
    emit ''
    emit '</details>'
    emit ''
  fi
fi

# ── Merge ref fallback warning ──
if [ "$MERGE_FALLBACK" = "true" ]; then
  emit '> [!WARNING]'
  emit '> **Merge ref unavailable** — this likely means the PR has merge conflicts with the target branch. The diff below compares the PR branch with its merge-base, so it shows the PR'"'"'s own changes, not the actual merge result. Please rebase or merge the target branch into your PR.'
  emit ''
fi

# ── ATLAS review reference ──
# How the caller references the review (root action max06/atlas@<ref>, or the
# deprecated reusable workflow). A release reference is the recommendation: it
# pins the action, the CLI and the image to one version.
#   @latest release          → silent
#   pin changed by PR        → neutral note (regardless of old/new version)
#   @SHA                     → silent (a commit is a valid pin)
#   @main                    → note: the development branch, pin a release
#   @older release           → note: a newer release exists
#   not detected             → silent (don't false-alarm on renamed/forked workflows)
is_sha() { [[ "$1" =~ ^[0-9a-f]{40}$ ]]; }

if [ -n "$WORKFLOW_PIN_MERGE" ] && ! is_sha "$WORKFLOW_PIN_MERGE"; then
  PIN_CHANGED=false
  [ -n "$WORKFLOW_PIN_TARGET" ] && [ "$WORKFLOW_PIN_TARGET" != "$WORKFLOW_PIN_MERGE" ] && PIN_CHANGED=true

  if [ "$PIN_CHANGED" = true ]; then
    emit '> [!NOTE]'
    emit "> This PR changes the ATLAS review reference: \`@${WORKFLOW_PIN_TARGET}\` → \`@${WORKFLOW_PIN_MERGE}\`."
    emit ''
  fi

  if [ "$WORKFLOW_PIN_MERGE" = "main" ]; then
    emit '> [!NOTE]'
    emit "> The review runs from \`max06/atlas@main\`, the development branch. Pin a release (\`@${LATEST_ATLAS_TAG:-v<version>}\`) for reproducible reviews."
    emit ''
  elif [ -n "$LATEST_ATLAS_TAG" ] && [ "$WORKFLOW_PIN_MERGE" != "$LATEST_ATLAS_TAG" ] && [ "$PIN_CHANGED" = false ]; then
    emit '> [!NOTE]'
    emit "> The review is pinned to \`@${WORKFLOW_PIN_MERGE}\`; the latest release is \`@${LATEST_ATLAS_TAG}\`."
    emit ''
  fi
fi

# ── Security messaging (selector-probe driven) ──
if [ "$BASELINE_FILTER" = "false" ] && [ "$PR_FILTER" = "false" ]; then
  emit '> [!CAUTION]'
  emit '> **Both sides use an older ATLAS version that leaks template-level secrets.** Values from SOPS files referenced inside app templates (`release[].values`, `release[].secrets`) may appear in cleartext in the diff below. Review carefully and rotate anything that leaks.'
  emit ''
elif [ "$BASELINE_FILTER" = "false" ]; then
  emit '> [!CAUTION]'
  emit '> **The target branch uses an older ATLAS version that leaks template-level secrets.** Values from SOPS files referenced inside app templates may appear in cleartext on the baseline side of the diff. Review carefully and rotate anything that leaks.'
  emit ''
elif [ "$PR_FILTER" = "false" ]; then
  emit '> [!CAUTION]'
  emit '> **This PR pins an older ATLAS version that leaks template-level secrets.** Values from SOPS files referenced inside app templates may appear in cleartext on the PR side of the diff. Review carefully and rotate anything that leaks.'
  emit ''
fi

# ── Baseline render error or missing (non-blocking) ──
if [ "$BASELINE_STATUS" = "error" ]; then
  emit '> [!WARNING]'
  emit '> **Target branch failed to render.** Diff is unavailable, but merging is not blocked — this PR may be the fix.'
  emit ''
  if [ -n "$BASELINE_ERRORS" ]; then
    emit '<details>'
    emit '<summary>Render errors (target branch)</summary>'
    emit ''
    emit '```'
    emit "$BASELINE_ERRORS"
    emit '```'
    emit ''
    emit '</details>'
    emit ''
  fi
elif [ "$BASELINE_STATUS" = "missing" ]; then
  emit '> [!NOTE]'
  emit "> Helmfile not found on the target branch (\`${HELMFILE_PATH}\`). This is expected if the PR introduces ATLAS to the repository."
  emit ''
fi

# ── Render scope (changed-file subsetting) ──
# Written by the action entrypoint from the classifier's output; absent when
# subsetting is off. States what was rendered (or, in shadow mode, what the
# classifier would have rendered) so a surprising "no changes" is auditable.
if [ -f "${COMMENT_TEMP}/scope.md" ]; then
  BODY="${BODY}$(cat "${COMMENT_TEMP}/scope.md")"$'\n'
  emit ''
fi

# ── Diff results ──
if [ "$PR_STATUS" = "error" ] && [ "$BASELINE_STATUS" != "success" ]; then
  emit 'Unable to generate a diff.'
elif [ "$DIFF_STATUS" = "no-changes" ]; then
  emit 'No changes detected in rendered Kubernetes manifests.'
elif [ "$DIFF_STATUS" = "changes" ]; then
  emit "Changes detected in **${DIFF_TOTAL}** resource(s) across **${DIFF_RELEASES}** release(s)."
  emit 'Please review before merging.'
  emit ''
  if [ "$DIFF_TRUNCATED" = "true" ] && [ -n "$RUN_URL" ]; then
    emit '> [!NOTE]'
    emit "> Some diffs were truncated to fit the PR comment size limit. See the [job summary](${RUN_URL}) for the full output."
    emit ''
  fi
  emit '### Affected releases'
  emit ''
  if [ -f "${COMMENT_TEMP}/comment-diff.md" ]; then
    BODY="${BODY}$(cat "${COMMENT_TEMP}/comment-diff.md")"$'\n'
  fi
elif [ "$DIFF_STATUS" = "empty" ]; then
  emit 'No releases were rendered on either side. Check your helmfile configuration if this is unexpected.'
fi

emit ''
emit '---'
if [ -n "$RUN_URL" ]; then
  emit "*Generated by [ATLAS](https://github.com/max06/atlas) Review — [Re-run](${RUN_URL})*"
else
  emit "*Generated by [ATLAS](https://github.com/max06/atlas) Review*"
fi

# Write the comment body using heredoc delimiter
{
  echo 'body<<ATLAS_EOF'
  printf '%s' "$BODY"
  echo 'ATLAS_EOF'
} >> "$GITHUB_OUTPUT"

# ── Job summary ─────────────────────────────────────────────────────────────
{
  echo '## ATLAS Review Summary'
  echo ''

  # ATLAS review reference table
  echo '### ATLAS review reference'
  echo ''
  echo '| Source | Pin |'
  echo '| --- | --- |'
  pin_cell() { if [ -z "$1" ]; then echo '—'; else echo "\`$1\`"; fi; }
  echo "| Target branch | $(pin_cell "$WORKFLOW_PIN_TARGET") |"
  echo "| Merge result | $(pin_cell "$WORKFLOW_PIN_MERGE") |"
  echo "| Upstream latest release | $(pin_cell "$LATEST_ATLAS_TAG") |"
  echo ''

  # Full diff (untruncated) from summary-diff.md
  if [ -f "${COMMENT_TEMP}/summary-diff.md" ] && [ -s "${COMMENT_TEMP}/summary-diff.md" ]; then
    echo '### Manifest diff'
    echo ''
    cat "${COMMENT_TEMP}/summary-diff.md"
  fi
} > "${COMMENT_TEMP}/summary.md"
