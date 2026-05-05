#!/usr/bin/env bats
#
# Scenario: hierarchy `.yaml.gotmpl` files reference .Values.atlas.* during
# the hierarchy walk.
#
# A deployment-level values.yaml.gotmpl (auto-loaded for the deployment's
# directory at hierarchy-walk time) needs access to .Values.atlas.deployment.*
# so it can compose values keyed by the deployment's cluster, name, etc.
# This is a real consumer pattern: e.g. compute domains, paths, or
# k8s-namespace values from the deployment context.
#
# atlas.hierarchy.merged builds the tpl context for hierarchy gotmpls. It
# must include .Values.atlas, not just the accumulating hierarchy. The
# accumulator starts empty and only ever holds hierarchy values, so atlas
# would be invisible without an explicit merge.
#
# Bug history: pre-extraction, atlas.values.merged had the same bug — the
# inlined hierarchy walk built $hCtx from $hierarchy alone. It was never
# user-visible because no fixture or production case exercised
# .Values.atlas in a hierarchy gotmpl until 2026-05-01.

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment42
RELEASE=stage3-hier-atlas-ref-release

setup_file() { ensure_rendered; }

@test "d42: deployment values.yaml.gotmpl resolves .Values.atlas.deployment.cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .fromHierarchyGotmpl.cluster
  [ "$output" = "cluster1" ]
}

@test "d42: deployment values.yaml.gotmpl resolves .Values.atlas.deployment.deploymentName" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .fromHierarchyGotmpl.deploymentName
  [ "$output" = "deployment42" ]
}
