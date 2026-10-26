#!/usr/bin/env bats
# The native engine's discovery map (`atlas discover --engine native`) must
# equal the map of the helmfile templates for every fixture root: the same
# pairs, deployment paths, templates and local charts. Needs ATLAS_CLI.

load '../helpers/subset'

setup() {
  [ -n "${ATLAS_CLI:-}" ] || skip "ATLAS_CLI not set"
  TEST_TEMP="$(mktemp -d "${BATS_FILE_TMPDIR}/native-discover.XXXXXX")"
}

# the template map, with an empty charts list made explicit (the CLI's JSON
# omits it)
_template_map() {
  (cd "$(_repo_root)/tests" && ATLAS_DISCOVERY_MAP=1 ATLAS_SKIP_SECRETS=true \
    helmfile -f helmfile.yaml.gotmpl build --allow-no-matching-release \
      --state-values-set "atlas.deploymentDefinitions=$1" 2>/dev/null) |
    yq -o json 'select(.renderedvalues.atlasDiscovery) | .renderedvalues.atlasDiscovery' |
    jq -S '.pairs |= map(. + {charts: (.charts // [])})'
}

_native_map() {
  "$ATLAS_CLI" discover --engine native -C "$(_repo_root)/tests" --deployments "$1" |
    jq -S '.pairs |= map(. + {charts: (.charts // [])})'
}

@test "native discover: equals the template map for every fixture root" {
  for root in deployments fixtures-negative fixtures-global-only; do
    _template_map "$root" > "$TEST_TEMP/template-$root.json"
    _native_map "$root" > "$TEST_TEMP/native-$root.json"
    [ -s "$TEST_TEMP/template-$root.json" ]
    run diff "$TEST_TEMP/template-$root.json" "$TEST_TEMP/native-$root.json"
    [ "$status" -eq 0 ] || { echo "root $root: $output"; false; }
  done
  # the fixture tree has templates and local charts, not only pairs
  [ "$(jq '[.pairs[].charts[]] | length' "$TEST_TEMP/native-deployments.json")" -gt 10 ]
}

@test "native discover: a duplicate leaf cluster name fails like the template" {
  run "$ATLAS_CLI" discover --engine native -C "$(_repo_root)/tests" --deployments fixtures-dup-leaf
  [ "$status" -ne 0 ]
  [[ "$output" == *'duplicate leaf cluster name "dupe" used by: group-a/dupe, group-b/dupe'* ]]
}
