#!/usr/bin/env bats
#
# Scenario: `atlas render --tree` runs the render tree of a recursive ATLAS
# repository like Argo CD builds it, and `--argocd` renders one child
# Application from its plugin environment.
#
# Fixture root tests/fixtures-tree (entry tests/helmfile.tree.yaml.gotmpl):
#   c1/apps/web       on-disk deployment (app1)
#   c1/apps/parent    parent (template app-children) with two children:
#     kid             c1, app1
#     nested          c1, app-children again, with one child:
#       grandkid      c2, app1
# tests/fixtures-tree-collision: the parent has a child named like c1/web.
#
# Uses $ATLAS_CLI when set, else builds the binary; without either the file
# is skipped.

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

_tree() {
  "$ATLAS_CLI" -C "$(_repo_root)/tests" -f "helmfile.$1.yaml.gotmpl" render "${@:2}"
}

@test "tree: renders the children down to the leaves" {
  run _tree tree --tree -o "$OUT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"atlas: tree: 3 child render(s)"* ]]
  [[ "$output" == *"c2/grandkid (via parent -> nested -> grandkid)"* ]]
  [ -f "$OUT/c1/kid/app1/chart1/templates/configmap.yaml" ]
  [ -f "$OUT/c1/nested/app-children/raw/templates/resources.yaml" ]
  [ -f "$OUT/c2/grandkid/app1/chart1/templates/configmap.yaml" ]
}

@test "tree: a child gets the hierarchy of its own cluster" {
  run _tree tree --tree -o "$OUT"
  [ "$status" -eq 0 ]
  run yq '.data.values.atlas.deployment | .cluster + "/" + .deploymentName' "$OUT/c2/grandkid/app1/chart1/templates/configmap.yaml"
  [ "$output" = "c2/grandkid" ]
}

@test "tree: without --tree only the discovered deployments render" {
  run _tree tree -o "$OUT"
  [ "$status" -eq 0 ]
  [ -d "$OUT/c1/parent" ]
  [ -d "$OUT/c1/web" ]
  [ ! -e "$OUT/c1/kid" ]
}

@test "tree: stdout mode prints the child manifests too" {
  run bash -c "'$ATLAS_CLI' -C '$(_repo_root)/tests' -f helmfile.tree.yaml.gotmpl render --tree 2>/dev/null | yq -N 'select(.kind == \"ConfigMap\") | .data.values.atlas.deployment.deploymentName' | sort | tr '\n' ' '"
  [ "$status" -eq 0 ]
  [ "$output" = "grandkid kid web " ]
}

@test "tree: a child with the name of a deployment on disk fails" {
  run _tree tree-collision --tree -o "$OUT"
  [ "$status" -ne 0 ]
  [[ "$output" == *"child c1/web (application argo-c1/web) has the cluster and name of a deployment on disk"* ]]
}

@test "tree: --tree and --argocd exclude each other" {
  ARGOCD_ENV_ATLAS_FILTER_CLUSTER=c1 ARGOCD_ENV_ATLAS_FILTER_DEPLOYMENT_NAME=web ARGOCD_APP_NAME=web \
    run _tree tree --tree --argocd
  [ "$status" -ne 0 ]
  [[ "$output" == *"--tree and --argocd exclude each other"* ]]
}

@test "argocd: a child Application renders from its plugin env" {
  ARGOCD_ENV_ATLAS_FILTER_CLUSTER=c2 ARGOCD_ENV_ATLAS_FILTER_DEPLOYMENT_NAME=grandkid \
    ARGOCD_ENV_ATLAS_DEPLOYMENT=$'apps:\n  - template: app1' \
    ARGOCD_ENV_ATLAS_DEPLOYMENT_BASE=fixtures-tree/c2/.children/grandkid \
    ARGOCD_ENV_ATLAS_PARENTS=parent,nested \
    ARGOCD_APP_NAME=grandkid XDG_CACHE_HOME="${BATS_TEST_TMPDIR}/cache" \
    run bash -c "'$ATLAS_CLI' -C '$(_repo_root)/tests' -f helmfile.tree.yaml.gotmpl render --argocd 2>/dev/null | yq -N 'select(.kind == \"ConfigMap\") | .data.values.atlas.deployment.deploymentName'"
  [ "$status" -eq 0 ]
  [ "$output" = "grandkid" ]
}

@test "argocd: a definition without a base fails" {
  ARGOCD_ENV_ATLAS_FILTER_CLUSTER=c2 ARGOCD_ENV_ATLAS_FILTER_DEPLOYMENT_NAME=grandkid \
    ARGOCD_ENV_ATLAS_DEPLOYMENT=$'apps:\n  - template: app1' ARGOCD_APP_NAME=grandkid \
    run _tree tree --argocd
  [ "$status" -ne 0 ]
  [[ "$output" == *"ARGOCD_ENV_ATLAS_DEPLOYMENT_BASE is not"* ]]
}

# _tree_repo builds a git repository from fixtures-tree (deployments, the
# test templates and charts) with one base commit; prints its path.
_tree_repo() {
  local root repo
  root="$(_repo_root)"
  repo="${BATS_TEST_TMPDIR}/tree-repo"
  mkdir -p "$repo"
  cp -r "$root/tests/fixtures-tree" "$repo/deployments"
  cp -r "$root/tests/templates" "$root/tests/charts" "$repo/"
  cat > "$repo/helmfile.yaml.gotmpl" <<EOT
helmfiles:
  - path: ${root}/helmfile.yaml.gotmpl
    values:
      - atlas:
          cwd: {{ exec "pwd" (list) | trim }}
          appTemplates: templates
          deploymentDefinitions: deployments
EOT
  git -C "$repo" init -q -b main
  git -C "$repo" add -A
  git -C "$repo" -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false commit -qm base
  echo "$repo"
}

@test "review: a change two levels deep in a parent shows the grandchild release" {
  local repo base
  repo="$(_tree_repo)"
  base="$(git -C "$repo" rev-parse HEAD)"
  sed -i 's/^                      - template: app1$/                      - template: app1\n                        values:\n                          - fromGrandkid: changed/' "$repo/deployments/c1/apps/parent/values.yaml"
  run --separate-stderr "$ATLAS_CLI" -C "$repo" review diff --base "$base"
  [ "$status" -eq 0 ]
  [[ "$stderr" == *"classify: mode=subset"* ]]
  [[ "$stderr" == *"review: 3 child render(s)"* ]]
  [ "$output" = $'changed  c1/nested/app-children\nchanged  c1/parent/app-children\nchanged  c2/grandkid/app1' ]
}

@test "review ci render: child renders are pairs of the side" {
  local tmp="${BATS_TEST_TMPDIR}/ci"
  mkdir -p "$tmp"
  ( cd "$(_repo_root)/tests" && GITHUB_OUTPUT="$tmp/out" HELMFILE_PATH=helmfile.tree.yaml.gotmpl SNAPSHOT_LABEL=pr RENDER_TEMP="$tmp" \
      "$ATLAS_CLI" review ci render >/dev/null 2>&1 )
  [ "$(grep '^status=' "$tmp/out")" = "status=success" ]
  [ "$(grep '^rendered_pairs=' "$tmp/out")" = "rendered_pairs=5" ]
  [ "$(sort "$tmp/pr-pairs.txt" | tr '\n' ' ')" = "c1|kid c1|nested c1|parent c1|web c2|grandkid " ]
  [ -f "$tmp/pr-snapshot/c2/grandkid/app1/chart1/templates/configmap.yaml" ]
}
