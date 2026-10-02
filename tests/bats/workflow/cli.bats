#!/usr/bin/env bats
# End-to-end tests of the atlas CLI (cmd/atlas) against the fixture repo.
#
# Uses $ATLAS_CLI when set (CI builds it once), else builds the binary with the
# local Go toolchain; without either the file is skipped.

bats_require_minimum_version 1.5.0

load '../helpers/subset'

setup_file() {
  # Every test reuses the one fixture repo (checkouts, uncommitted edits).
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
  git -C "$FIXTURE_REPO" checkout -q --detach "$BASE_SHA"
  git -C "$FIXTURE_REPO" reset -q --hard
  git -C "$FIXTURE_REPO" clean -qfd
}

@test "cli: render equals plain helmfile with the production flags and the same filter" {
  local root; root="$(_repo_root)"
  run --separate-stderr "$ATLAS_CLI" -C "$root/tests" render -c cluster1 -d deployment1
  [ "$status" -eq 0 ]
  local cli="$output"
  run --separate-stderr env ATLAS_FILTER_CLUSTER=cluster1 ATLAS_FILTER_DEPLOYMENT_NAME=deployment1 \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" template --skip-schema-validation --include-crds \
    --args "--skip-schema-validation --include-crds" --selector cluster=cluster1,deploymentName=deployment1
  [ "$status" -eq 0 ]
  [ -n "$cli" ]
  [ "$cli" = "$output" ]
}

@test "cli: ATLAS_* in the caller's environment is ignored (flags are the interface)" {
  local root; root="$(_repo_root)"
  run --separate-stderr env ATLAS_FILTER_DEPLOYMENT_NAME=deployment1 "$ATLAS_CLI" -C "$root/tests" discover
  [ "$status" -eq 0 ]
  [[ "$stderr" == *"ignoring ATLAS_FILTER_DEPLOYMENT_NAME"* ]]
  # the env filter did not narrow the map
  [ "$(jq '[.pairs[].deploymentName] | unique | length' <<< "$output")" -gt 1 ]
}

@test "cli: discover prints the same map as discover.sh" {
  local root; root="$(_repo_root)"
  run --separate-stderr "$ATLAS_CLI" -C "$root/tests" discover
  [ "$status" -eq 0 ]
  local cli="$output"
  ( cd "$root/tests" && HELMFILE_PATH=helmfile.yaml.gotmpl "$(_actions_dir)/atlas-render/discover.sh" "$BATS_TEST_TMPDIR/map.json" >/dev/null )
  [ "$(jq -S . <<< "$cli")" = "$(jq -S . "$BATS_TEST_TMPDIR/map.json")" ]
}

@test "cli: review diff of an uncommitted edit renders and reports only that release" {
  printf 'apps:\n  - template: app1\n    namespace: test\n    values:\n      - cliMarker: changed\n' \
    > "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  local before; before="$(git -C "$FIXTURE_REPO" status --porcelain)"
  run --separate-stderr "$ATLAS_CLI" -C "$FIXTURE_REPO" review diff --base "$BASE_SHA" --offline
  [ "$status" -eq 0 ]
  [[ "$stderr" == *"classify: mode=subset"* ]]
  [ "$output" = "changed  cluster1/deployment1/app1" ]
  # the working tree snapshot did not touch the index or the status
  [ "$(git -C "$FIXTURE_REPO" status --porcelain)" = "$before" ]
}

@test "cli: review classify merges onto the current base (newer base commits are not reported)" {
  git -C "$FIXTURE_REPO" checkout -q -B feature "$BASE_SHA"
  echo "# feature" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment3/deployment.yaml"
  fixture_commit "$FIXTURE_REPO" "feature edit"
  git -C "$FIXTURE_REPO" checkout -q -B target "$BASE_SHA"
  echo "# target moved on" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  fixture_commit "$FIXTURE_REPO" "target edit"
  git -C "$FIXTURE_REPO" checkout -q feature

  run --separate-stderr "$ATLAS_CLI" -C "$FIXTURE_REPO" review classify --base target --head feature --offline
  [ "$status" -eq 0 ]
  [ "$(jq -c '.selected' <<< "$output")" = '["cluster1|deployment3"]' ]
  # --no-merge compares the branches directly: the target's edit shows up as well
  run --separate-stderr "$ATLAS_CLI" -C "$FIXTURE_REPO" review classify --base target --head feature --offline --no-merge
  [ "$(jq -c '.selected' <<< "$output")" = '["cluster1|deployment1","cluster1|deployment3"]' ]
}

@test "cli: review classify reports merge conflicts instead of guessing" {
  git -C "$FIXTURE_REPO" checkout -q -B feature "$BASE_SHA"
  echo "# feature" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  fixture_commit "$FIXTURE_REPO" "feature edit"
  git -C "$FIXTURE_REPO" checkout -q -B target "$BASE_SHA"
  echo "# target" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  fixture_commit "$FIXTURE_REPO" "target edit"

  run --separate-stderr "$ATLAS_CLI" -C "$FIXTURE_REPO" review classify --base target --head feature --offline
  [ "$status" -ne 0 ]
  [[ "$stderr" == *"conflicts in: deployments/cluster1/apps/deployment1/deployment.yaml"* ]]
}
