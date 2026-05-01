#!/usr/bin/env bats
#
# Scenario: ATLAS exposes two complementary filter paths and they must stay
# observable from the consumer side.
#
#   1. CLI `--selector` — standard helmfile flag, matches release-level
#      commonLabels (cluster, clusterName, clusterGroup, deploymentName,
#      variant). Filters AFTER every sub-helmfile is parsed, so it does NOT
#      avoid SOPS decryption — but it's what an interactive operator reaches
#      for and must not be silently overridden by ATLAS.
#
#   2. Stage-1 env vars `ATLAS_FILTER_CLUSTER` / `ATLAS_FILTER_DEPLOYMENT_NAME`
#      — short-circuit in templates/helmfile.all.yaml.gotmpl that drops
#      non-matching clusters/deployments before any sub-helmfile is even
#      emitted. This is the SOPS-friendly path used by the ArgoCD
#      ApplicationSet pattern (one ATLAS render per Application).
#
# Regression history: v0.3.0 added a hardcoded `selectors:` field on every
# emitted sub-helmfile entry. In stock helmfile (without the unmerged
# helmfile/helmfile#2545), child `selectors:` REPLACE the inherited CLI
# selector when filtering releases inside that sub-helmfile, so the CLI flag
# became a no-op. v0.3.0 also deleted the stage-1 env path on the same
# assumption. Both paths were restored; these tests pin the two contracts.

load 'helpers/render'

# helmfile_list runs `helmfile list --output json` against the test fixtures
# with the given extra args (selectors, etc.) and emits the JSON array on
# stdout. `--allow-no-matching-release` keeps zero-match cases from exiting
# non-zero so we can assert empty results explicitly. stderr is dropped so
# bats's `run` (which captures both streams into $output) doesn't pick up
# helmfile's "err: no releases found..." informational line and break the
# downstream JSON-parse assertions for empty-result selectors.
helmfile_list() {
  local root
  root="$(_repo_root)"
  helmfile -f "$root/helmfile.yaml.gotmpl" \
    --state-values-set "atlas.appTemplates=tests/templates" \
    --state-values-set "atlas.deploymentDefinitions=tests/deployments" \
    --state-values-set "atlas.cwd=$root" \
    --allow-no-matching-release \
    list --output json "$@" 2>/dev/null
}

@test "selector: --selector cluster=does-not-exist returns zero releases" {
  # Pin the v0.3.0 regression: with the hardcoded child selectors, every
  # release passed its self-tautological filter and the CLI flag was ignored.
  # Empty result here proves CLI --selector reaches release-level labels.
  run helmfile_list --selector "cluster=does-not-exist"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | yq 'length')" = "0" ]
}

@test "selector: --selector cluster=cluster1 returns only cluster1 releases" {
  run helmfile_list --selector "cluster=cluster1"
  [ "$status" -eq 0 ]
  # Every returned release must carry cluster=cluster1 in its labels (the
  # labels JSON field is a comma-joined "k:v,k:v" string from helmfile list).
  local mismatched
  mismatched="$(echo "$output" | yq '[.[] | select(.labels | test("(^|,)cluster:cluster1(,|$)") | not)] | length')"
  [ "$mismatched" = "0" ]
  # And at least one release matched, so the assertion above isn't vacuous.
  [ "$(echo "$output" | yq 'length')" != "0" ]
}

@test "selector: --selector deploymentName=deployment1 returns only deployment1 releases" {
  run helmfile_list --selector "deploymentName=deployment1"
  [ "$status" -eq 0 ]
  local mismatched
  mismatched="$(echo "$output" | yq '[.[] | select(.labels | test("(^|,)deploymentName:deployment1(,|$)") | not)] | length')"
  [ "$mismatched" = "0" ]
  [ "$(echo "$output" | yq 'length')" != "0" ]
}

@test "stage-1 env: ATLAS_FILTER_CLUSTER drops non-matching clusters pre-emit" {
  # No --selector here — only the env-var path is in play. Every emitted
  # release must belong to cluster1; releases from other clusters must not
  # appear because their sub-helmfiles weren't even generated.
  local output status
  output="$(ATLAS_FILTER_CLUSTER=cluster1 helmfile_list)"
  status=$?
  [ "$status" -eq 0 ]
  local mismatched
  mismatched="$(echo "$output" | yq '[.[] | select(.labels | test("(^|,)cluster:cluster1(,|$)") | not)] | length')"
  [ "$mismatched" = "0" ]
  [ "$(echo "$output" | yq 'length')" != "0" ]
}

@test "stage-1 env: ATLAS_FILTER_DEPLOYMENT_NAME drops non-matching deployments pre-emit" {
  local output status
  output="$(ATLAS_FILTER_DEPLOYMENT_NAME=deployment1 helmfile_list)"
  status=$?
  [ "$status" -eq 0 ]
  local mismatched
  mismatched="$(echo "$output" | yq '[.[] | select(.labels | test("(^|,)deploymentName:deployment1(,|$)") | not)] | length')"
  [ "$mismatched" = "0" ]
  [ "$(echo "$output" | yq 'length')" != "0" ]
}

@test "stage-1 env: ATLAS_FILTER_CLUSTER + ATLAS_FILTER_DEPLOYMENT_NAME narrow to a single deployment" {
  local output status
  output="$(ATLAS_FILTER_CLUSTER=cluster1 ATLAS_FILTER_DEPLOYMENT_NAME=deployment1 helmfile_list)"
  status=$?
  [ "$status" -eq 0 ]
  # All emitted releases must match both filters; some deployments produce
  # multiple releases, so don't assert exact count — assert all-match.
  local mismatched
  mismatched="$(echo "$output" | yq '[.[] | select((.labels | test("(^|,)cluster:cluster1(,|$)")) and (.labels | test("(^|,)deploymentName:deployment1(,|$)")) | not)] | length')"
  [ "$mismatched" = "0" ]
  [ "$(echo "$output" | yq 'length')" != "0" ]
}
