#!/usr/bin/env bash
# Runs the review container: `atlas review ci action review` in the atlas
# image, the way GitHub runs a docker action, but with the image chosen at run
# time (see setup.sh) and as the runner's user, so nothing in the checkout
# ends up owned by root.
#
# Mounts: the workspace and RUNNER_TEMP at their own paths (the review switches
# the checkout in place; the summary file path stays valid on the host), a
# HOME under RUNNER_TEMP at /github/home (the CLI keeps the helm and helmfile
# caches there), and GITHUB_OUTPUT (the CLI writes the step outputs). The
# inputs travel as environment variables, never on the command line.
set -euo pipefail

: "${IMAGE:?setup.sh resolves the image}"
workspace="${GITHUB_WORKSPACE:-$PWD}"
temp="${RUNNER_TEMP:-/tmp}"
home="${temp}/atlas-home"
output="${GITHUB_OUTPUT:?}"
mkdir -p "$home"
touch "$output"

docker pull -q "$IMAGE" >/dev/null

args=(
  run --rm
  --user "$(id -u):$(id -g)"
  --workdir "$workspace"
  --volume "${workspace}:${workspace}"
  --volume "${temp}:${temp}"
  --volume "${home}:/github/home"
  --volume "${output}:/github/output"
  --env GITHUB_ACTIONS=true
  --env CI=true
  --env HOME=/github/home
  --env "RUNNER_TEMP=${temp}"
  --env GITHUB_OUTPUT=/github/output
  --env "GITHUB_WORKSPACE=${workspace}"
)
# Values come from this process's environment (--env NAME), not from the
# command line, so the SOPS key never shows in a process list.
for name in INPUT_HELMFILE_PATH INPUT_BASE INPUT_SUBSET_MODE INPUT_DIFF_MODE INPUT_SOPS_AGE_KEY INPUT_KEYLESS INPUT_ENGINE_REPOSITORY \
            INPUT_MERGE_FALLBACK INPUT_LATEST_ATLAS_TAG INPUT_LATEST_MAIN_SHA INPUT_RUN_URL INPUT_IN_PLACE \
            RENDER_PARALLEL HELMFILE_TIMEOUT RUNNER_DEBUG ACTIONS_STEP_DEBUG \
            GITHUB_REPOSITORY GITHUB_SERVER_URL GITHUB_RUN_ID GITHUB_SHA GITHUB_REF GITHUB_EVENT_NAME; do
  if [ -n "${!name:-}" ]; then
    args+=(--env "$name")
  fi
done

# A side that pins another engine renders in that release's image, started
# by the review as a sibling container through the docker socket (the same
# same-path mounts make the paths valid there too).
socket=/var/run/docker.sock
if [ -S "$socket" ]; then
  args+=(--volume "${socket}:${socket}" --group-add "$(stat -c %g "$socket")")
fi

exec docker "${args[@]}" "$IMAGE" review ci action review
