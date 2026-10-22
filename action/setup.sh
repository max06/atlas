#!/usr/bin/env bash
# Resolves the review image and the subset mode.
#
# Image: input `image`, else derived from the reference this action was used
# with (github.action_ref): a tag or branch maps to the image tag the image
# workflow pushes for it (tags as they are, branches with "/" as "-"), a commit
# SHA to its sha-<7> tag. From a local path (./) no reference exists.
set -euo pipefail

image="${INPUT_IMAGE:-}"
if [ -z "$image" ]; then
  ref="${ACTION_REF:-}"
  if [ -z "$ref" ]; then
    echo "::error::input 'image' is required when the action runs from a local path (github.action_ref is empty)"
    exit 1
  fi
  if [[ "$ref" =~ ^[0-9a-f]{40}$ ]]; then
    tag="sha-${ref:0:7}"
  else
    tag="${ref//\//-}"
  fi
  image="ghcr.io/max06/atlas:${tag}"
fi
echo "review image: $image"
echo "image=$image" >> "$GITHUB_OUTPUT"

mode="${REQUESTED:-off}"
case "$mode" in
  off | shadow | on) ;;
  *) echo "::warning::render-subset='$mode' unknown — using off"; mode=off ;;
esac
if [ "${LABELED:-false}" = "true" ]; then
  echo "PR carries the full-render label — subsetting off for this run"
  mode=off
fi
echo "subset=$mode" >> "$GITHUB_OUTPUT"
