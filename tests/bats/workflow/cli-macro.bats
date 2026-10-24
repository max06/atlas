#!/usr/bin/env bats
#
# Scenario: macros (deploymentTemplates). A deployment lists macros under
# deploymentTemplates; a macro is a template directory with a
# deployment.yaml.gotmpl that renders (with the macro values and the
# hierarchy) into an apps list. Each entry becomes a component
# <deployment>-<entry>, rendered as a child; the deployment itself renders
# only its parent template (atlas.macroParentTemplate of the entry).
#
# Fixture root tests/fixtures-macro (entry tests/helmfile.macro.yaml.gotmpl):
#   c1/apps/plain   ordinary deployment (app1)
#   c1/apps/svc     deploymentTemplates: macro-service (name: shop,
#                   dbConnectionPoolSize: 200) → components db, app (needs
#                   db), project (destination mgmt)
# Parent template: tests/templates/app-macro-parent.

_repo_root() { cd "${BATS_TEST_DIRNAME}/../../.." && pwd; }

setup_file() {
  if [ -z "${ATLAS_CLI:-}" ] && command -v go >/dev/null; then
    export ATLAS_CLI="${BATS_FILE_TMPDIR}/atlas"
    ( cd "$(_repo_root)" && go build -o "$ATLAS_CLI" ./cmd/atlas )
  fi
}

setup() {
  [ -n "${ATLAS_CLI:-}" ] || skip "no ATLAS_CLI and no Go toolchain"
  OUT="${BATS_TEST_TMPDIR}/out"
}

_render() {
  "$ATLAS_CLI" -C "$(_repo_root)/tests" -f "helmfile.$1.yaml.gotmpl" render "${@:2}"
}

@test "macro: a deployment with a macro renders its components as children" {
  run _render macro --tree -o "$OUT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"atlas: tree: 3 child render(s)"* ]]
  [ -f "$OUT/c1/svc/app-macro-parent/raw/templates/resources.yaml" ]
  [ -f "$OUT/c1/svc-db/db-app1/chart1/templates/configmap.yaml" ]
  [ -f "$OUT/c1/svc-app/app-app1/chart1/templates/configmap.yaml" ]
  [ -f "$OUT/c1/svc-project/project-app1/chart1/templates/configmap.yaml" ]
  [ -f "$OUT/c1/plain/app1/chart1/templates/configmap.yaml" ]
}

@test "macro: the component gets the macro values and the hierarchy" {
  run _render macro --tree -o "$OUT"
  [ "$status" -eq 0 ]
  run yq -N 'select(.kind == "ConfigMap") | .data.values | [.poolSize, .serviceName, .fromHierarchy, .atlas.deployment.deploymentName] | map(tostring) | join(" ")' \
    "$OUT/c1/svc-db/db-app1/chart1/templates/configmap.yaml"
  [ "$output" = "200 shop global svc-db" ]
}

@test "macro: the parent template gets destination and needs per component" {
  run _render macro -o "$OUT"
  [ "$status" -eq 0 ]
  run yq -N 'select(.kind == "Application") | .metadata.name + " " + .spec.destination.name + " " + .metadata.annotations."atlas.io/needs"' \
    "$OUT/c1/svc/app-macro-parent/raw/templates/resources.yaml"
  [ "$output" = $'svc-db c1 \nsvc-app c1 svc-db\nsvc-project mgmt ' ]
}

@test "macro: the discovery map lists the macro, the parent template and the entry templates" {
  run bash -c "ATLAS_DISCOVERY_MAP=1 helmfile -f '$(_repo_root)/tests/helmfile.macro.yaml.gotmpl' build --allow-no-matching-release 2>/dev/null | yq eval-all -o=json '[.]' - | jq -r '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0].pairs[] | select(.deploymentName == \"svc\") | .templates | join(\",\")'"
  [ "$status" -eq 0 ]
  [ "$output" = "app-macro-parent,app1,macro-service" ]
}

@test "macro: without atlas.macroParentTemplate the render fails" {
  run _render macro-noparent -c c1 -d svc
  [ "$status" -ne 0 ]
  [[ "$output" == *"sets no atlas.macroParentTemplate"* ]]
}

@test "macro: two entries with the same name fail" {
  run _render macro-dup -c c1 -d svc
  [ "$status" -ne 0 ]
  [[ "$output" == *'two macro entries with the name "app1"'* ]]
}

@test "macro: a change of a macro value shows in the review as a component change" {
  local root repo base
  root="$(_repo_root)"
  repo="${BATS_TEST_TMPDIR}/repo"
  mkdir -p "$repo"
  cp -r "$root/tests/fixtures-macro" "$repo/deployments"
  cp -r "$root/tests/templates" "$root/tests/charts" "$repo/"
  cat > "$repo/helmfile.yaml.gotmpl" <<EOT
helmfiles:
  - path: ${root}/helmfile.yaml.gotmpl
    values:
      - atlas:
          cwd: {{ exec "pwd" (list) | trim }}
          appTemplates: templates
          deploymentDefinitions: deployments
          macroParentTemplate: app-macro-parent
EOT
  git -C "$repo" init -q -b main
  git -C "$repo" add -A
  git -C "$repo" -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false commit -qm base
  base="$(git -C "$repo" rev-parse HEAD)"
  sed -i 's/dbConnectionPoolSize: 200/dbConnectionPoolSize: 300/' "$repo/deployments/c1/apps/svc/deployment.yaml"
  run --separate-stderr "$ATLAS_CLI" -C "$repo" review diff --base "$base"
  [ "$status" -eq 0 ]
  [[ "$stderr" == *"classify: mode=subset"* ]]
  [[ "$output" == *"changed  c1/svc-db/db-app1"* ]]
  [[ "$output" != *"c1/svc-app/"* ]]
  [[ "$output" != *"c1/plain/"* ]]
}
