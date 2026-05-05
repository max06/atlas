#!/usr/bin/env bats
#
# Scenario: chart default values (from chart1/values.yaml) land in the rendered
# output and survive the redaction pipeline.
#
# chart1/values.yaml defines `aChartValue: on default`. This is the lowest-
# priority source in helm's value chain — any hierarchy or template value with
# the same key would override it. No template or fixture overrides aChartValue,
# so the chart default should appear verbatim in every rendered release.

load 'helpers/render'

setup_file() { ensure_rendered; }

@test "chart default aChartValue appears in plain render" {
  run get_path cluster1 deployment1 app1 .aChartValue
  [ "$output" = "on default" ]
}

@test "chart default aChartValue also appears on a grouped-cluster render" {
  run get_path group1/cluster2 deployment2 app1 .aChartValue
  [ "$output" = "on default" ]
}
