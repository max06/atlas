#!/usr/bin/env bats
#
# Scenario: an app template ships a local Helm chart in a sibling directory
# (./manifests). ATLAS must resolve the relative chart: path against the
# template's own directory so helm can locate the chart. Without that resolver
# the path is interpreted against helm's CWD (repo root) and the render fails.
#
# Fixture layout:
#   tests/templates/app-localchart/
#     helmfile.yaml.gotmpl     chart: ./manifests
#     manifests/
#       Chart.yaml             name: chart1
#       values.yaml            localChartMarker: from-local-chart
#       templates/configmap.yaml
#
# Assertion: the local chart's values.yaml default reaches the rendered output,
# which proves both that the chart was located and that its defaults merged.

load '../helpers/render'

setup_file() { ensure_rendered; }

@test "local chart at ./manifests is located and rendered" {
  run instance_rendered cluster1 deployment14 app-localchart
  [ "$status" -eq 0 ]
}

@test "local chart default value appears in rendered output" {
  run get_path cluster1 deployment14 app-localchart .localChartMarker
  [ "$output" = "from-local-chart" ]
}
