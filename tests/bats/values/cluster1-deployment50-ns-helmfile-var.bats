#!/usr/bin/env bats
#
# Scenario: app template referencing helmfile's NATIVE state-template
# variable `.Namespace` (the value of the `--namespace` CLI flag, empty
# string when the flag is absent). Plain helmfile provides this key in
# every state and values template, so templates written against plain
# helmfile may reference it.
#
# Regression: ATLAS renders app templates via `tpl` with hand-built
# contexts (state-build pass in helmfile.instance.yaml.gotmpl AND the
# release-time values-loader pass in _values_loader.tpl). Both contexts
# used to lack the Namespace key, hard-failing the whole render with
# `map has no entry for key "Namespace"` (helmfile's tpl runs with
# missingkey=error). The fix mirrors helmfile's native .Namespace into
# both contexts.
#
# The bulk render runs without --namespace, so .Namespace must resolve
# to the empty string — exactly what plain helmfile would produce.

load '../helpers/render'

setup_file() { ensure_rendered; }

@test "app template using {{ .Namespace }} renders (empty without --namespace flag)" {
  run get_path cluster1 deployment50 ns-helmfile-var .helmfileNamespaceVar
  [ "$output" = "cli[]" ]
}

@test "scenario value from the same template survives alongside .Namespace usage" {
  run get_path cluster1 deployment50 ns-helmfile-var .scenario
  [ "$output" = "helmfile-native-namespace-var" ]
}
