#!/usr/bin/env bats
#
# Scenario: an app template points chart: at a sibling directory that contains
# only plain Kubernetes manifests — no Chart.yaml, no kustomization.yaml.
# helmfile's chartify auto-wraps such bare manifest dirs into a synthetic
# chart, so once ATLAS resolves the relative chart: path to absolute, the
# render just works.
#
# Fixture layout:
#   tests/templates/app-localmanifests/
#     helmfile.yaml.gotmpl     chart: ./manifests
#     manifests/
#       configmap.yaml         a plain manifest, no kustomization or chart metadata

load 'helpers/render'

setup_file() { ensure_rendered; }

@test "local plain-manifests dir at ./manifests is located and rendered" {
  run instance_rendered_any cluster1 deployment16 app-localmanifests
  [ "$status" -eq 0 ]
}

@test "plain manifest content appears in rendered output" {
  run render_contains cluster1 deployment16 app-localmanifests "marker: from-plain-manifests"
  [ "$status" -eq 0 ]
}
