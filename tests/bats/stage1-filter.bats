#!/usr/bin/env bats
#
# Scenario: ATLAS's optional stage-1 filter narrows the emitted helmfiles: list
# BEFORE helmfile processes it, so a single-deployment render (ArgoCD
# ApplicationSet pattern) skips value-decryption and templating for every
# other deployment in the repo. Driven by --state-values-set atlas.filter.*
# because helmfile doesn't expose --selector to templates.
#
# These tests run their own helmfile invocations (no ensure_rendered) since
# they need distinct state-values per test. Speed-wise they're cheap — the
# filter guarantees only 1 sub-helmfile gets loaded.

_repo_root() {
  cd "${BATS_TEST_DIRNAME}/../.." && pwd
}

_helmfile_build() {
  # $1=cluster filter (or empty)  $2=deployment filter (or empty)
  local cluster="$1" deployment="$2" root
  root="$(_repo_root)"
  local args=(
    -f "$root/helmfile.yaml.gotmpl"
    --state-values-set "atlas.appTemplates=tests/templates"
    --state-values-set "atlas.deploymentDefinitions=tests/deployments"
    --state-values-set "atlas.cwd=$root"
  )
  [[ -n "$cluster" ]]    && args+=(--state-values-set "atlas.filter.cluster=$cluster")
  [[ -n "$deployment" ]] && args+=(--state-values-set "atlas.filter.deploymentName=$deployment")
  helmfile "${args[@]}" build 2>/dev/null
}

_release_count() {
  _helmfile_build "$1" "$2" | yq 'select(.releases != null) | .releases | length' | awk '{s+=$1} END {print s+0}'
}

_release_names() {
  _helmfile_build "$1" "$2" | yq 'select(.releases != null) | .releases[].name' | sort -u
}

# Baseline: without filter, the build pulls every deployment. Used to prove
# the filter actually drops things rather than, e.g., breaking the scan.
@test "stage1: no filter loads the full repo (baseline)" {
  run _release_count "" ""
  [ "$status" -eq 0 ]
  # The repo has many deployments across cluster1 and group1/cluster{2,3}.
  # Assert the count is clearly >1 (exact number drifts as fixtures evolve).
  [ "$output" -gt 5 ]
}

@test "stage1: filter by cluster drops other clusters" {
  run _release_count "cluster1" ""
  [ "$status" -eq 0 ]
  local full
  full="$(_release_count "" "")"
  # Fewer releases than the unfiltered baseline.
  [ "$output" -lt "$full" ]
  [ "$output" -gt 0 ]
}

@test "stage1: filter by cluster + deploymentName renders exactly that deployment" {
  # deployment1 on cluster1 uses app1 → exactly one release named app1.
  run _release_names "cluster1" "deployment1"
  [ "$status" -eq 0 ]
  [ "$output" = "app1" ]
}

@test "stage1: filter by a grouped-cluster path resolves the group slash" {
  # group1/cluster2 deployment2 → app1 release.
  run _release_names "group1/cluster2" "deployment2"
  [ "$status" -eq 0 ]
  [ "$output" = "app1" ]
}

@test "stage1: non-matching cluster filter emits an empty helmfile list" {
  run _release_count "does-not-exist" ""
  [ "$status" -eq 0 ]
  [ "$output" -eq 0 ]
}

@test "stage1: filter key does not leak into chart values" {
  # Render deployment1 with the filter set, then inspect the chart1 ConfigMap
  # that echoes .Values into its data.values. The filter key is internal to
  # stage 1 and must be stripped before the atlas context is passed to
  # sub-helmfiles — otherwise the filter values would leak into rendered
  # manifests and diverge from an unfiltered render.
  local root out cm_file
  root="$(_repo_root)"
  out="$(mktemp -d)"
  helmfile -f "$root/helmfile.yaml.gotmpl" \
    --state-values-set "atlas.appTemplates=tests/templates" \
    --state-values-set "atlas.deploymentDefinitions=tests/deployments" \
    --state-values-set "atlas.cwd=$root" \
    --state-values-set "atlas.filter.cluster=cluster1" \
    --state-values-set "atlas.filter.deploymentName=deployment1" \
    template --skip-schema-validation \
    --output-dir "$out" >/dev/null 2>&1
  cm_file="$(find "$out" -name 'configmap.yaml' | head -1)"
  [ -n "$cm_file" ]
  # Chart1 ConfigMap echoes .Values to .data.values. filter key must be absent.
  run grep -c 'filter:' "$cm_file"
  rm -rf "$out"
  [ "$output" = "0" ]
}

@test "stage1: filter skips sub-helmfiles entirely (broken fixture is not loaded)" {
  # Create a temporary deployment that references a non-existent template.
  # Without the filter, rendering the repo would fail. With a filter that
  # excludes it, rendering must succeed — proving helmfile never touches
  # the bad sub-helmfile.
  local root broken_dir
  root="$(_repo_root)"
  broken_dir="$root/tests/deployments/cluster1/apps/_broken_filter_probe"
  mkdir -p "$broken_dir"
  cat > "$broken_dir/deployment.yaml" <<'EOF'
apps:
  - template: this-template-does-not-exist
    namespace: test
EOF
  # Cleanup handler via trap-like idiom: remove on test exit regardless of pass/fail.
  # bats' teardown doesn't fire on setup_file failures, so remove inline.
  run _release_count "cluster1" "deployment1"
  rm -rf "$broken_dir"
  [ "$status" -eq 0 ]
  # Exactly one release (app1 from deployment1). If helmfile had loaded the
  # broken deployment's sub-helmfile it would have errored instead.
  [ "$output" -eq 1 ]
}
