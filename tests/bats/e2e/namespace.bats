#!/usr/bin/env bats
# e2e/namespace.bats — namespace handling across the ATLAS pipeline.
#
# Tests namespace precedence:
#   default (none) < template release.namespace < settings.namespace < apps[].namespace
#
# The --namespace CLI flag (injected by the helmfile-argocd-plugin) is
# OUTSIDE ATLAS's control — helmfile applies it after state-file
# processing. The fix for that path is in the ArgoCD ApplicationSet
# (HELMFILE_USE_CONTEXT_NAMESPACE=true). Tests here document that
# limitation explicitly.
#
# Cluster: in-cluster (tests/deployments/in-cluster/)
# Templates: app-ns-fixed, app-ns-bare, app-ns-multi

# render_ns renders a single deployment and extracts all namespace: values
# from the output. Returns one namespace per line.
render_ns() {
  local deployment="$1"
  shift
  local root
  root="$(cd "$(dirname "${BATS_TEST_FILENAME}")"/../.. && pwd)"
  helmfile template -f "$root/helmfile.yaml.gotmpl" \
    --skip-schema-validation \
    --selector "cluster=in-cluster,deploymentName=${deployment}" \
    "$@" 2>/dev/null | \
    grep '^\s*namespace:' | awk '{print $2}'
}

# =====================================================================
# Template namespace (no override)
#
# Template: app-ns-fixed (namespace: fixed-namespace)
# Deployment: ns-fixed (no settings.namespace, no apps[].namespace)
# =====================================================================

@test "ns-fixed: template namespace is preserved when no override" {
  run render_ns ns-fixed
  [ "$status" -eq 0 ]
  [ "$output" = "fixed-namespace" ]
}

@test "ns-fixed: --namespace flag overrides template namespace (helmfile limitation)" {
  # helmfile applies --namespace AFTER state-file processing — ATLAS
  # cannot prevent this. The fix is on the ArgoCD side:
  # HELMFILE_USE_CONTEXT_NAMESPACE=true in the ApplicationSet.
  run render_ns ns-fixed --namespace injected-ns
  [ "$status" -eq 0 ]
  [ "$output" = "injected-ns" ]
}

# =====================================================================
# apps[].namespace overrides template namespace
#
# Template: app-ns-fixed (namespace: fixed-namespace)
# Deployment: ns-override (apps[].namespace: override-namespace)
# =====================================================================

@test "ns-override: apps[].namespace overrides template namespace" {
  run render_ns ns-override
  [ "$status" -eq 0 ]
  [ "$output" = "override-namespace" ]
}

# =====================================================================
# Multi-namespace template with apps[].namespace override
#
# Template: app-ns-multi
#   release ns-multi-main:  namespace: main-namespace
#   release ns-multi-extra: namespace: extra-namespace
# Deployment: ns-multi (apps[].namespace: override-namespace)
# =====================================================================

@test "ns-multi: apps[].namespace overrides all release namespaces" {
  run render_ns ns-multi
  [ "$status" -eq 0 ]
  local count
  count=$(echo "$output" | wc -l)
  [ "$count" -eq 2 ]
  [ "$(echo "$output" | sed -n '1p')" = "override-namespace" ]
  [ "$(echo "$output" | sed -n '2p')" = "override-namespace" ]
}

# =====================================================================
# Bare template (no namespace in template)
#
# Template: app-ns-bare (no namespace on release)
# Deployment: ns-default (no settings.namespace, no apps[].namespace)
# =====================================================================

@test "ns-default: release without namespace inherits from context" {
  run render_ns ns-default
  [ "$status" -eq 0 ]
  [ -n "$output" ]
}

@test "ns-default: --namespace flag sets namespace for bare template" {
  run render_ns ns-default --namespace deployment-name
  [ "$status" -eq 0 ]
  [ "$output" = "deployment-name" ]
}

# =====================================================================
# settings.namespace — deployment-wide namespace default
#
# Template: app-ns-bare (no namespace on release)
# Deployment: ns-settings (settings.namespace: settings-namespace)
# =====================================================================

@test "ns-settings: settings.namespace sets namespace on bare template" {
  run render_ns ns-settings
  [ "$status" -eq 0 ]
  [ "$output" = "settings-namespace" ]
}

# =====================================================================
# settings.namespace overrides template namespace
#
# Template: app-ns-fixed (namespace: fixed-namespace)
# Deployment: ns-settings-fixed (settings.namespace: settings-namespace)
# =====================================================================

@test "ns-settings-fixed: settings.namespace overrides template namespace" {
  run render_ns ns-settings-fixed
  [ "$status" -eq 0 ]
  [ "$output" = "settings-namespace" ]
}

# =====================================================================
# settings.namespace on multi-release template
#
# Template: app-ns-multi (two releases with different namespaces)
# Deployment: ns-settings-multi (settings.namespace: settings-namespace)
# =====================================================================

@test "ns-settings-multi: settings.namespace overrides all release namespaces" {
  run render_ns ns-settings-multi
  [ "$status" -eq 0 ]
  local count
  count=$(echo "$output" | wc -l)
  [ "$count" -eq 2 ]
  [ "$(echo "$output" | sed -n '1p')" = "settings-namespace" ]
  [ "$(echo "$output" | sed -n '2p')" = "settings-namespace" ]
}

# =====================================================================
# apps[].namespace overrides settings.namespace
#
# Template: app-ns-bare (no namespace on release)
# Deployment: ns-settings-override
#   (settings.namespace: settings-namespace, apps[].namespace: instance-namespace)
# =====================================================================

@test "ns-settings-override: apps[].namespace beats settings.namespace" {
  run render_ns ns-settings-override
  [ "$status" -eq 0 ]
  [ "$output" = "instance-namespace" ]
}
