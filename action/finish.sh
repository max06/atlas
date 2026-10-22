#!/usr/bin/env bash
# Writes the job summary and gates on the render status of the merge result.
set -euo pipefail

if [ -n "${SUMMARY_FILE:-}" ] && [ -f "$SUMMARY_FILE" ]; then
  cat "$SUMMARY_FILE" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
fi
if [ "${REVIEW_OUTCOME:-}" != "success" ]; then
  echo "::error::The ATLAS review did not complete (step outcome: ${REVIEW_OUTCOME:-unknown}). Read the step log."
  exit 1
fi
if [ "${PR_STATUS:-}" = "error" ] && [ "${FAIL_ON_RENDER_ERROR:-true}" = "true" ]; then
  echo "::error::The merge result failed to render — read the ATLAS Review comment on the PR."
  exit 1
fi
echo "ATLAS review passed. Read the diff in the PR comment before you merge."
