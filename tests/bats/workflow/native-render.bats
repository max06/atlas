#!/usr/bin/env bats
# Render-level corner cases of the native engine against the template engine
# (fixtures-native-render): .Release at release time as helmfile prepares it
# (commonLabels over release labels, the kubeContext default of helmDefaults,
# release fields rendered at load time), two releases with one name in two
# namespaces, a map-shaped strategicMergePatches. The same helmfile call runs
# through the real template engine and through the native-engine shim; the
# outputs must be identical. Needs ATLAS_CLI.

load '../helpers/subset'

setup() {
  [ -n "${ATLAS_CLI:-}" ] || skip "ATLAS_CLI not set"
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/native-render.XXXXXX")"
  SHIM_DIR="$(_repo_root)/tests/bats/helpers/native-engine"
}

_render() {
  helmfile -f "$(_repo_root)/tests/helmfile.yaml.gotmpl" template --skip-schema-validation \
    --state-values-set "atlas.deploymentDefinitions=fixtures-native-render/deployments,atlas.appTemplates=fixtures-native-render/templates" 2>/dev/null
}

@test "native render: equals the template engine on the render corner cases" {
  ATLAS_NATIVE_SHIM_OFF=1 PATH="$SHIM_DIR:$PATH" _render > "$TEST_TEMP/template.yaml"
  PATH="$SHIM_DIR:$PATH" _render > "$TEST_TEMP/native.yaml"
  [ -s "$TEST_TEMP/template.yaml" ]
  run diff "$TEST_TEMP/template.yaml" "$TEST_TEMP/native.yaml"
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  # not vacuous: the cases are in the output
  grep -q 'cluster: g1/c1' "$TEST_TEMP/native.yaml"
  grep -q 'kubeContext: kc-default' "$TEST_TEMP/native.yaml"
  grep -q 'namespace: c1-ns' "$TEST_TEMP/native.yaml"
  grep -q 'namespace: nsb' "$TEST_TEMP/native.yaml"
  grep -q 'patched: "yes"' "$TEST_TEMP/native.yaml"
}

@test "native render --redact: equals the review's render step with redaction in the CLI" {
  # A: the review's render step (helmfile engine, maps from helmfile build,
  # snapshot.RedactTree) through the side contract; B: atlas render
  # --engine native --redact (maps from the native states, same scrub)
  local root; root="$(_repo_root)"
  cat > "$TEST_TEMP/req.json" <<JSON
{"contract": 1, "step": "render", "dir": "$root", "entry": "tests/helmfile.yaml.gotmpl", "label": "pr",
 "temp": "$TEST_TEMP/a", "sidedumpDir": "$TEST_TEMP/a-maps", "redactInCLI": true,
 "sopsAgeKeyFile": "$SOPS_AGE_KEY_FILE", "parallel": 4}
JSON
  mkdir -p "$TEST_TEMP/a"
  (cd "$root" && "$ATLAS_CLI" review ci side --request "$TEST_TEMP/req.json" --result "$TEST_TEMP/res.json" >/dev/null 2>&1)
  [ "$(jq -r '.render.status' "$TEST_TEMP/res.json")" = "success" ]
  (cd "$root/tests" && "$ATLAS_CLI" render --engine native --redact --output-dir "$TEST_TEMP/b" >/dev/null 2>&1)
  run diff -r "$(jq -r '.render.snapshotDir' "$TEST_TEMP/res.json")" "$TEST_TEMP/b"
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  # not vacuous: maps applied, Secrets hashed, no plaintext of a test secret
  run grep -rq 'AKIAIOSFODNN7EXAMPLE' "$TEST_TEMP/b"
  [ "$status" -eq 1 ]
  grep -rq 'REDACTED' "$TEST_TEMP/b"
}

# a copy of the test tree that renders with the native engine (atlas.yaml
# spec.native); rendered values embed the tree path, so the comparison
# replaces both tree paths with TREE
_native_tree() {
  cp -r "$(_repo_root)/tests" "$1"
  printf 'apiVersion: atlas.io/v1alpha1\nkind: Root\nspec:\n  engine: "0.14"\n  native: true\n' > "$1/atlas.yaml"
}

_side() { # <step> <dir> <entry> <temp> [extra json fields]
  mkdir -p "$4"
  cat > "$4/$1.req.json" <<JSON
{"contract": 1, "step": "$1", "dir": "$2", "entry": "$3", "label": "pr", "temp": "$4",
 "mapFile": "$4/map.json", "sidedumpDir": "$4/maps", "redactInCLI": true,
 "sopsAgeKeyFile": "$SOPS_AGE_KEY_FILE", "parallel": 4 $5}
JSON
  (cd "$2" && "$ATLAS_CLI" review ci side --request "$4/$1.req.json" --result "$4/$1.res.json" >"$4/$1.log" 2>&1) || true
}

_same_tree() { # <a> <a-root> <b> <b-root>
  local f n=0
  while read -r f; do
    n=$((n+1))
    cmp -s <(sed "s#$2#TREE#g" "$1/$f") <(sed "s#$4#TREE#g" "$3/$f") || { echo "differs: $f"; return 1; }
  done < <(cd "$1" && find . -type f | sort)
  [ "$n" -eq "$(cd "$3" && find . -type f | wc -l)" ] || { echo "file count: $n vs $(cd "$3" && find . -type f | wc -l)"; return 1; }
  [ "$n" -gt 0 ]
}

@test "review side on a native tree: discovery map and redacted render equal the helmfile side" {
  local root; root="$(_repo_root)"
  _native_tree "$TEST_TEMP/tree"
  _side discover "$root" tests/helmfile.yaml.gotmpl "$TEST_TEMP/a"
  _side discover "$TEST_TEMP/tree" helmfile.yaml.gotmpl "$TEST_TEMP/b"
  grep -q 'discover (native engine)' "$TEST_TEMP/b/discover.log"
  local q='.pairs|map({cluster,deploymentName,templates,charts})'
  diff <(jq -S "$q" "$TEST_TEMP/a/map.json") <(jq -S "$q" "$TEST_TEMP/b/map.json")
  _side render "$root" tests/helmfile.yaml.gotmpl "$TEST_TEMP/a"
  _side render "$TEST_TEMP/tree" helmfile.yaml.gotmpl "$TEST_TEMP/b"
  [ "$(jq -r .render.status "$TEST_TEMP/a/render.res.json")" = success ]
  [ "$(jq -r .render.status "$TEST_TEMP/b/render.res.json")" = success ]
  grep -q 'engine of the merge result: native' "$TEST_TEMP/b/render.log"
  _same_tree "$TEST_TEMP/a/pr-snapshot" "$root/tests" "$TEST_TEMP/b/pr-snapshot" "$TEST_TEMP/tree"
  # the maps the baseline replay reads
  diff -r "$TEST_TEMP/a/maps" "$TEST_TEMP/b/maps"
  grep -rq 'REDACTED' "$TEST_TEMP/b/pr-snapshot"
  run grep -rq 'AKIAIOSFODNN7EXAMPLE' "$TEST_TEMP/b/pr-snapshot"
  [ "$status" -eq 1 ]
}

@test "review side on a native tree: a broken pair fails alone and leaves nothing in the snapshot" {
  _native_tree "$TEST_TEMP/tree"
  echo '{{ fail "boom" }}' >> "$TEST_TEMP/tree/deployments/cluster1/apps/deployment1/values.yaml.gotmpl"
  _side render "$TEST_TEMP/tree" helmfile.yaml.gotmpl "$TEST_TEMP/b"
  [ "$(jq -r .render.status "$TEST_TEMP/b/render.res.json")" = error ]
  grep -q '::warning::Render failed for cluster1/deployment1 on merge result' "$TEST_TEMP/b/render.log"
  [ ! -e "$TEST_TEMP/b/pr-snapshot/cluster1/deployment1" ]
  # the other pairs rendered
  [ -f "$TEST_TEMP/b/pr-snapshot/cluster1/deployment12/app1/chart1/templates/configmap.yaml" ]
  [ -f "$TEST_TEMP/b/pr-snapshot/group1/cluster2/deployment8/app-multi-first/chart1/templates/configmap.yaml" ]
}

@test "review side on a native tree: subset render equals the helmfile side" {
  local root; root="$(_repo_root)"
  _native_tree "$TEST_TEMP/tree"
  printf 'cluster1|deployment47\ngroup1/cluster2|deployment8\ncluster1|deployment20\n' > "$TEST_TEMP/pairs.txt"
  _side render "$root" tests/helmfile.yaml.gotmpl "$TEST_TEMP/a" ", \"pairsFile\": \"$TEST_TEMP/pairs.txt\""
  _side render "$TEST_TEMP/tree" helmfile.yaml.gotmpl "$TEST_TEMP/b" ", \"pairsFile\": \"$TEST_TEMP/pairs.txt\""
  [ "$(jq -r .render.mode "$TEST_TEMP/b/render.res.json")" = subset ]
  [ "$(jq -r .render.renderedPairs "$TEST_TEMP/b/render.res.json")" = 3 ]
  _same_tree "$TEST_TEMP/a/pr-snapshot" "$root/tests" "$TEST_TEMP/b/pr-snapshot" "$TEST_TEMP/tree"
  diff -r "$TEST_TEMP/a/maps" "$TEST_TEMP/b/maps"
}
