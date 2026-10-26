#!/usr/bin/env bats
# The native engine's discovery map (`atlas discover --engine native`) must
# equal the map of the helmfile templates: the same pairs, deployment paths,
# templates and local charts, and the same failures. fixtures-native-edge
# holds the corner cases (dotted leaf names, apps as a map, exec and relative
# file functions in the templates base path, a false template value, a glob
# part that is no valid regex, the skipSecrets switch); each
# fixtures-native-err-* root must fail on both engines. Needs ATLAS_CLI.

load '../helpers/subset'

setup() {
  [ -n "${ATLAS_CLI:-}" ] || skip "ATLAS_CLI not set"
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/native-discover.XXXXXX")"
}

# $1 = deployments root, $2 = templates root (relative to tests/). The
# template map gets no ATLAS_* switch, like `atlas discover`; an empty charts
# list is made explicit (the CLI's JSON omits it).
_template_map() {
  (cd "$(_repo_root)/tests" && ATLAS_DISCOVERY_MAP=1 \
    helmfile -f helmfile.yaml.gotmpl build --allow-no-matching-release \
      --state-values-set "atlas.deploymentDefinitions=$1,atlas.appTemplates=$2" 2>"$TEST_TEMP/template.err") |
    yq -o json 'select(.renderedvalues.atlasDiscovery) | .renderedvalues.atlasDiscovery' |
    jq -S '.pairs |= map(. + {charts: (.charts // [])})'
  return "${PIPESTATUS[0]}"
}

_native_map() {
  "$ATLAS_CLI" discover --engine native -C "$(_repo_root)/tests" --deployments "$1" --templates "$2" 2>"$TEST_TEMP/native.err" |
    jq -S '.pairs |= map(. + {charts: (.charts // [])})'
  return "${PIPESTATUS[0]}"
}

@test "native discover: equals the template map for every fixture root" {
  for spec in deployments:templates fixtures-negative:templates fixtures-global-only:templates \
              fixtures-native-edge/deployments:fixtures-native-edge/templates; do
    root="${spec%%:*}" templates="${spec#*:}" name="${root//\//-}"
    _template_map "$root" "$templates" > "$TEST_TEMP/template-$name.json"
    _native_map "$root" "$templates" > "$TEST_TEMP/native-$name.json"
    [ -s "$TEST_TEMP/template-$name.json" ]
    run diff "$TEST_TEMP/template-$name.json" "$TEST_TEMP/native-$name.json"
    [ "$status" -eq 0 ] || { echo "root $root: $output"; false; }
  done
  # the fixture tree has templates and local charts, not only pairs
  [ "$(jq '[.pairs[].charts[]] | length' "$TEST_TEMP/native-deployments.json")" -gt 10 ]
  # the edge root: both dotted leaves are clusters, a map of apps renders
  [ "$(jq '[.pairs[] | select(.clusterName == "eu.prod")] | length' "$TEST_TEMP/native-fixtures-native-edge-deployments.json")" -eq 2 ]
  [ "$(jq -r '.pairs[] | select(.deploymentName == "mapapps") | .templates[0]' "$TEST_TEMP/native-fixtures-native-edge-deployments.json")" = "t1" ]
}

@test "native discover: every error root fails on both engines" {
  for spec in fixtures-dup-leaf:templates fixtures-native-err-empty:templates \
              fixtures-native-err-dotted:templates fixtures-native-err-chartnull:fixtures-native-err-chartnull/templates; do
    root="${spec%%:*}" templates="${spec#*:}"
    run _template_map "$root" "$templates"
    [ "$status" -ne 0 ] || { echo "template accepted $root"; false; }
    run _native_map "$root" "$templates"
    [ "$status" -ne 0 ] || { echo "native accepted $root: $output"; false; }
  done
  run "$ATLAS_CLI" discover --engine native -C "$(_repo_root)/tests" --deployments fixtures-dup-leaf
  [[ "$output" == *'duplicate leaf cluster name "dupe" used by: group-a/dupe, group-b/dupe'* ]]
}
