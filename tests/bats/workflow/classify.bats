#!/usr/bin/env bats
# Scenario tests for .github/actions/atlas-render/classify.sh (+ discover.sh).
#
# Each test mutates a fixture repo built from tests/deployments + tests/templates,
# commits, and asserts which (cluster, deployment) pairs the classifier selects
# for baseline (HEAD~1) and PR (HEAD). Expected sets are derived from the
# discovery maps with jq, so the tests do not hardcode fixture counts.
#
# The one property every scenario must hold: a change that can alter a
# rendered release selects that release's pair (or forces a full render).
# Over-selection is tolerated and asserted only where it is the point.

load '../helpers/subset'

setup_file() {
  export FIXTURE_REPO="${BATS_FILE_TMPDIR}/consumer"
  make_fixture_repo "$FIXTURE_REPO"
  export BASE_SHA
  BASE_SHA="$(git -C "$FIXTURE_REPO" rev-parse HEAD)"
}

setup() {
  # every test starts from the base commit with a clean tree
  git -C "$FIXTURE_REPO" checkout -q --detach "$BASE_SHA"
  git -C "$FIXTURE_REPO" clean -qfd
  OUT="$(mktemp -d "${BATS_FILE_TMPDIR}/classify.XXXXXX")"
}

# run_scenario <message> — commit the working tree and classify base..head
run_scenario() {
  fixture_commit "$FIXTURE_REPO" "$1"
  classify_revisions "$FIXTURE_REPO" "${BASE_SHA}" HEAD "$OUT"
}

assert_subset() { [ "$(gh_output "$OUT" mode)" = "subset" ]; }
assert_full()   { [ "$(gh_output "$OUT" mode)" = "full" ]; [[ "$(gh_output "$OUT" reason)" == *"$1"* ]]; }
assert_pairs_pr()       { [ "$(sorted_file "$OUT/pairs-pr.txt")" = "$1" ]; }
assert_pairs_baseline() { [ "$(sorted_file "$OUT/pairs-baseline.txt")" = "$1" ]; }

# ── deployment-scoped changes ──────────────────────────────────────────────

@test "classify: cluster-level deployment edit selects exactly that pair" {
  echo "# touched" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  run_scenario "edit cluster1/deployment1"
  assert_subset
  assert_pairs_pr "cluster1|deployment1"
  assert_pairs_baseline "cluster1|deployment1"
}

@test "classify: group-level deployment edit fans out to every cluster of the group" {
  echo "# touched" >> "$FIXTURE_REPO/deployments/group1/apps/deployment6/deployment.yaml"
  run_scenario "edit group1/deployment6"
  assert_subset
  local expected
  expected="$(pairs_where "$OUT/map-pr.json" '.deploymentName=="deployment6" and (.cluster|startswith("group1/"))')"
  [ "$(wc -l <<< "$expected")" -ge 2 ]
  assert_pairs_pr "$expected"
  assert_pairs_baseline "$expected"
}

@test "classify: global deployment edit fans out to every cluster" {
  echo "# touched" >> "$FIXTURE_REPO/deployments/apps/deployment5/deployment.yaml"
  run_scenario "edit global deployment5"
  assert_subset
  local expected
  expected="$(pairs_where "$OUT/map-pr.json" '.deploymentName=="deployment5"')"
  [ "$(wc -l <<< "$expected")" -ge 3 ]
  assert_pairs_pr "$expected"
}

@test "classify: a values file next to deployment.yaml selects that pair" {
  echo "extra: 1" > "$FIXTURE_REPO/deployments/cluster1/apps/deployment3/values.yaml"
  run_scenario "add values.yaml to cluster1/deployment3"
  assert_subset
  assert_pairs_pr "cluster1|deployment3"
}

@test "classify: deployment switching template selects the pair (edge change)" {
  sed -i 's/template: app1$/template: app-novals/' "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  grep -q 'template: app-novals' "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  run_scenario "switch template"
  assert_subset
  assert_pairs_pr "cluster1|deployment1"
  # the PR map reflects the new edge
  [ "$(jq -c '.pairs[] | select(.cluster=="cluster1" and .deploymentName=="deployment1") | .templates' "$OUT/map-pr.json")" = '["app-novals"]' ]
}

# ── hierarchy values ──────────────────────────────────────────────────────

@test "classify: cluster values edit selects every deployment of that cluster only" {
  echo "touched: 1" >> "$FIXTURE_REPO/deployments/cluster1/cluster.values.yaml"
  run_scenario "edit cluster1 values"
  assert_subset
  local expected
  expected="$(pairs_where "$OUT/map-pr.json" '.cluster=="cluster1"')"
  [ "$(wc -l <<< "$expected")" -ge 10 ]
  assert_pairs_pr "$expected"
}

@test "classify: group values edit selects every deployment of every cluster in the group" {
  echo "touched: 1" >> "$FIXTURE_REPO/deployments/group1/group.values.yaml"
  run_scenario "edit group1 values"
  assert_subset
  assert_pairs_pr "$(pairs_where "$OUT/map-pr.json" '.cluster|startswith("group1/")')"
}

@test "classify: global values edit forces a full render" {
  echo "touched: 1" >> "$FIXTURE_REPO/deployments/global.values.yaml"
  run_scenario "edit global values"
  assert_full "global hierarchy file"
  [ ! -s "$OUT/pairs-pr.txt" ]
}

# ── templates ─────────────────────────────────────────────────────────────

@test "classify: template edit selects every deployment instantiating it, on both sides" {
  echo "# touched" >> "$FIXTURE_REPO/templates/app-novals-b/helmfile.yaml.gotmpl"
  run_scenario "edit template app-novals-b"
  assert_subset
  local expected
  expected="$(pairs_where "$OUT/map-pr.json" '.templates | index("app-novals-b")')"
  [ -n "$expected" ]
  assert_pairs_pr "$expected"
  assert_pairs_baseline "$expected"
}

@test "classify: edit of an unused template selects nothing (nothing renders it)" {
  mkdir -p "$FIXTURE_REPO/templates/app-unused"
  echo "releases: []" > "$FIXTURE_REPO/templates/app-unused/helmfile.yaml.gotmpl"
  run_scenario "add unused template"
  assert_subset
  [ ! -s "$OUT/pairs-pr.txt" ]
}

@test "classify: template in a subdirectory is matched by its full path, not the first segment" {
  # templates/<dir>/<app>/ referenced as `template: <dir>/<app>` — the template
  # name spans directories, so the classifier must not stop at `<dir>`.
  mkdir -p "$FIXTURE_REPO/templates/nested/app-nested" "$FIXTURE_REPO/deployments/cluster1/apps/deployment-nested"
  printf 'releases:\n  - name: app-nested\n    chart: ../../../charts/chart1\n    namespace: test\n' \
    > "$FIXTURE_REPO/templates/nested/app-nested/helmfile.yaml.gotmpl"
  printf 'apps:\n  - template: nested/app-nested\n' > "$FIXTURE_REPO/deployments/cluster1/apps/deployment-nested/deployment.yaml"
  fixture_commit "$FIXTURE_REPO" "add nested template + deployment"
  local mid; mid="$(git -C "$FIXTURE_REPO" rev-parse HEAD)"

  echo "# touched" >> "$FIXTURE_REPO/templates/nested/app-nested/helmfile.yaml.gotmpl"
  fixture_commit "$FIXTURE_REPO" "edit nested template"
  classify_revisions "$FIXTURE_REPO" "$mid" HEAD "$OUT"
  assert_subset
  # the map names the template by its full path...
  [ "$(jq -r '.pairs[] | select(.deploymentName=="deployment-nested") | .templates[]' "$OUT/map-pr.json")" = "nested/app-nested" ]
  # ...and the classifier selects by that name, not by "nested"
  assert_pairs_pr "cluster1|deployment-nested"
  assert_pairs_baseline "cluster1|deployment-nested"
  [ "$(jq -r '.changes[0].detail' "$OUT/classify.json")" = "nested/app-nested" ]
}

@test "classify: file directly in the templates root forces a full render" {
  echo "notes" > "$FIXTURE_REPO/templates/README.md"
  run_scenario "templates root file"
  assert_full "templates root"
}

# ── structural changes ────────────────────────────────────────────────────

@test "classify: cluster-level deployment shadowing a group one selects that cluster's pair" {
  mkdir -p "$FIXTURE_REPO/deployments/group1/cluster2/apps/deployment6"
  cp "$FIXTURE_REPO/deployments/group1/apps/deployment6/deployment.yaml" \
     "$FIXTURE_REPO/deployments/group1/cluster2/apps/deployment6/deployment.yaml"
  run_scenario "shadow deployment6 on cluster2"
  assert_subset
  assert_pairs_pr "group1/cluster2|deployment6"
  assert_pairs_baseline "group1/cluster2|deployment6"
  # the PR map now points cluster2's deployment6 at the cluster-level file
  [ "$(jq -r '.pairs[] | select(.cluster=="group1/cluster2" and .deploymentName=="deployment6") | .deploymentPath' "$OUT/map-pr.json")" = "deployments/group1/cluster2/apps/deployment6/deployment.yaml" ]
}

@test "classify: removed deployment is selected on the baseline and absent on the PR side" {
  git -C "$FIXTURE_REPO" rm -rq deployments/cluster1/apps/deployment3
  run_scenario "remove cluster1/deployment3"
  assert_subset
  assert_pairs_baseline "cluster1|deployment3"
  [ ! -s "$OUT/pairs-pr.txt" ]
}

@test "classify: new cluster selects its inherited group/global deployments too (one-side-only rule)" {
  mkdir -p "$FIXTURE_REPO/deployments/group1/cluster4/apps/newapp"
  printf 'apps:\n  - template: app-novals\n    namespace: test\n' > "$FIXTURE_REPO/deployments/group1/cluster4/apps/newapp/deployment.yaml"
  run_scenario "add cluster4"
  assert_subset
  local expected
  expected="$(pairs_where "$OUT/map-pr.json" '.cluster=="group1/cluster4"')"
  # newapp + group1's deployments + the global one → more than the one changed path implies
  [ "$(wc -l <<< "$expected")" -ge 3 ]
  grep -q '^group1/cluster4|deployment6$' <<< "$expected"
  assert_pairs_pr "$expected"
  [ ! -s "$OUT/pairs-baseline.txt" ]
}

@test "classify: renamed cluster directory selects the old pairs on baseline and the new on PR" {
  git -C "$FIXTURE_REPO" mv deployments/cluster1 deployments/cluster9
  run_scenario "rename cluster1 → cluster9"
  assert_subset
  assert_pairs_baseline "$(pairs_where "$OUT/map-baseline.json" '.cluster=="cluster1"')"
  assert_pairs_pr       "$(pairs_where "$OUT/map-pr.json"       '.cluster=="cluster9"')"
}

@test "classify: mixed change unions the per-path selections" {
  echo "# touched" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  echo "# touched" >> "$FIXTURE_REPO/templates/app-novals-b/helmfile.yaml.gotmpl"
  run_scenario "deployment + template"
  assert_subset
  local expected
  expected="$( { echo "cluster1|deployment1"; pairs_where "$OUT/map-pr.json" '.templates | index("app-novals-b")'; } | sort -u)"
  assert_pairs_pr "$expected"
}

# ── default-deny ──────────────────────────────────────────────────────────

@test "classify: file outside deployments/templates forces a full render" {
  echo "docs" > "$FIXTURE_REPO/README.md"
  run_scenario "readme"
  assert_full "outside deployments/templates"
}

@test "classify: chart change forces a full render (charts are outside the convention)" {
  echo "# touched" >> "$FIXTURE_REPO/charts/chart1/Chart.yaml"
  run_scenario "chart edit"
  assert_full "outside deployments/templates"
}

@test "classify: entry helmfile change forces a full render" {
  echo "# touched" >> "$FIXTURE_REPO/helmfile.yaml.gotmpl"
  run_scenario "entry edit"
  assert_full "entry helmfile"
}

@test "classify: a file directly under apps/ selects nothing" {
  echo "notes" > "$FIXTURE_REPO/deployments/cluster1/apps/README.md"
  run_scenario "apps dir file"
  assert_subset
  [ ! -s "$OUT/pairs-pr.txt" ]
  [ "$(jq -r '.changes[0].rule' "$OUT/classify.json")" = "apps-dir-file" ]
}

@test "classify: missing map on one side forces a full render" {
  echo "# touched" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  run_scenario "edit with old atlas baseline"
  : > "$OUT/empty-map.json"
  CLASSIFY_OUT="$OUT/again" MAP_BASELINE="$OUT/empty-map.json" MAP_PR="$OUT/map-pr.json" \
    HELMFILE_PATH=helmfile.yaml.gotmpl CHANGES_FILE="$OUT/changes.txt" GITHUB_OUTPUT="$OUT/again-output" \
    "$(_actions_dir)/atlas-render/classify.sh" >/dev/null
  [ "$(grep '^mode=' "$OUT/again-output" | cut -d= -f2)" = "full" ]
  grep -q 'no discovery map on the target branch' "$OUT/again-output"
}

@test "classify: comment fragment states the scope" {
  echo "# touched" >> "$FIXTURE_REPO/deployments/cluster1/apps/deployment1/deployment.yaml"
  run_scenario "fragment"
  grep -q '^\*\*Render scope:\*\* 1 of [0-9]* deployments (merge result)' "$OUT/classify-summary.md"
  grep -q 'deployments/cluster1/apps/deployment1/deployment.yaml' "$OUT/classify-summary.md"
}
