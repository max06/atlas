#!/usr/bin/env bats
#
# Scenario: an app template points chart: at a sibling directory containing a
# kustomization.yaml plus its resources. helmfile's chartify wraps the
# kustomize build output into a chart for helm, so as long as ATLAS resolves
# the relative chart: path to absolute, the render succeeds and the
# kustomize-produced manifests (including kustomize commonLabels) appear in
# the output.
#
# Fixture layout:
#   tests/templates/app-localkustomize/
#     helmfile.yaml.gotmpl     chart: ./manifests
#     manifests/
#       kustomization.yaml     resources + commonLabels
#       configmap.yaml         a plain manifest resource

load '../helpers/render'

setup_file() { ensure_rendered; }

@test "local kustomize dir at ./manifests is located and rendered" {
  run instance_rendered_any cluster1 deployment15 app-localkustomize
  [ "$status" -eq 0 ]
}

@test "kustomize-produced manifest content appears in rendered output" {
  run render_contains cluster1 deployment15 app-localkustomize "marker: from-kustomize"
  [ "$status" -eq 0 ]
}

@test "kustomize commonLabels applied (proves kustomize actually ran)" {
  run render_contains cluster1 deployment15 app-localkustomize "origin: kustomize"
  [ "$status" -eq 0 ]
}
