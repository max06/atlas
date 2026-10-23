#!/usr/bin/env bash
# Resolves the two commits of the review.
#
# Pull request (default): head = HEAD of the checkout (GitHub's merge ref),
# base = its first parent, the target commit the merge was made against. A
# separately fetched target tip can be newer and would show newer target
# commits as reverted by the PR. Without a merge ref (conflicts) the PR branch
# is compared with its merge-base from the compare API.
#
# Explicit `head`/`base` inputs: the two revisions as given, compared directly.
#
# The checkout keeps no credentials: the one fetch below carries the token as
# a header scoped to that command.
set -euo pipefail

fetch() {
  git -c "http.${SERVER_URL}/.extraheader=AUTHORIZATION: basic $(printf 'x-access-token:%s' "$GH_TOKEN" | base64 -w0)" \
    fetch -q --depth=1 origin "$1"
}
resolve() { git rev-parse -q --verify "$1^{commit}" 2>/dev/null || true; }

merge_fallback=false
if [ -n "${INPUT_HEAD:-}" ] || [ -n "${INPUT_BASE:-}" ]; then
  if [ -z "${INPUT_BASE:-}" ]; then
    echo "::error::input 'base' is required together with 'head'"
    exit 1
  fi
  head="$(resolve "${INPUT_HEAD:-HEAD}")"
  if [ -z "$head" ]; then
    echo "::error::head revision '${INPUT_HEAD}' not found in the checkout"
    exit 1
  fi
  base="$(resolve "$INPUT_BASE")"
  if [ -z "$base" ]; then
    fetch "$INPUT_BASE" && base="$(git rev-parse FETCH_HEAD)"
  fi
  echo "Revisions: base $base, head $head (explicit)"
else
  head="$(git rev-parse HEAD)"
  if [ "${MERGE_CHECKOUT:-}" = "success" ] && git rev-parse -q --verify 'HEAD^2' >/dev/null; then
    base="$(git rev-parse 'HEAD^1')"
    echo "Baseline: merge^1 = $base"
  else
    merge_fallback=true
    base="$(gh api "repos/${REPO}/compare/${PR_BASE_SHA}...${PR_HEAD_SHA}" --jq '.merge_base_commit.sha' 2>/dev/null || true)"
    if [ -n "$base" ] && fetch "$base"; then
      echo "Baseline: merge-base of the PR = $base"
    else
      echo "::warning::Could not resolve the merge-base — comparing with the target branch tip"
      fetch "refs/heads/${PR_BASE_REF}" && base="$(git rev-parse FETCH_HEAD)"
    fi
  fi
fi
{
  echo "head=$head"
  echo "base=$base"
  echo "merge_fallback=$merge_fallback"
} >> "$GITHUB_OUTPUT"
