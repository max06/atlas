#!/usr/bin/env bash
# Integration test for ATLAS post-renderer secret redaction.
#
# Runs `helmfile template` with redactSecrets=true and verifies that:
# - Tainted string values are replaced with "REDACTED"
# - Short numbers (< 5 digits) are preserved
# - Booleans are preserved
# - Non-tainted values are preserved
# - ATLAS infrastructure keys (deploymentName, paths) are not damaged
#
# Usage: ./tests/test-redaction.sh
# Requires: HELM_PLUGINS set to include the atlas-redact plugin directory

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PLUGIN_DIR="$REPO_ROOT/templates/helm-plugin"

# Ensure HELM_PLUGINS includes the atlas-redact plugin
DEFAULT_PLUGINS=$(helm env HELM_PLUGINS 2>/dev/null || echo "")
export HELM_PLUGINS="${DEFAULT_PLUGINS:+${DEFAULT_PLUGINS}:}${PLUGIN_DIR}"

PASS=0
FAIL=0
ERRORS=""

assert_contains() {
  local label="$1"
  local pattern="$2"
  local input="$3"
  if echo "$input" | grep -qF "$pattern"; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    ERRORS="${ERRORS}\n  FAIL: ${label}\n    expected to find: ${pattern}"
  fi
}

assert_not_contains() {
  local label="$1"
  local pattern="$2"
  local input="$3"
  if echo "$input" | grep -qF "$pattern"; then
    FAIL=$((FAIL + 1))
    ERRORS="${ERRORS}\n  FAIL: ${label}\n    expected NOT to find: ${pattern}"
  else
    PASS=$((PASS + 1))
  fi
}

echo "Running redaction integration tests..."
echo ""

# ── Test deployment1 on cluster1 (standalone, no group) ──────────────────────

echo "Testing cluster1/deployment1..."

OUTPUT=$(ATLAS_REDACT_SECRETS=true helmfile -f "$REPO_ROOT/helmfile.tests.yaml.gotmpl" template \
  --selector "cluster=cluster1,deploymentName=deployment1" \
  --skip-schema-validation \
  2>/dev/null) || {
  echo "FAIL: helmfile template failed for cluster1/deployment1"
  exit 1
}

# Tainted strings must be redacted
assert_contains "sopsGlobal redacted"         "sopsGlobal: REDACTED"          "$OUTPUT"
assert_contains "sopsString redacted"         "sopsString: REDACTED"          "$OUTPUT"
assert_contains "sopsCluster redacted"        "sopsCluster: REDACTED"         "$OUTPUT"
assert_contains "sopsDeployment redacted"     "sopsDeployment: REDACTED"      "$OUTPUT"
assert_contains "sopsOverride redacted"       "sopsOverride: REDACTED"        "$OUTPUT"
assert_contains "sopsMap.password redacted"   "password: REDACTED"            "$OUTPUT"
assert_contains "sopsMap.username redacted"   "username: REDACTED"            "$OUTPUT"
assert_contains "sopsNested.secretKey redacted"  "secretKey: REDACTED"        "$OUTPUT"
assert_contains "sopsNested.verySecret redacted" "verySecret: REDACTED"       "$OUTPUT"

# Pointer-tainted gotmpl values must be redacted
assert_contains "gotmplFromSops redacted"              "gotmplFromSops: REDACTED"              "$OUTPUT"
assert_contains "clusterGotmplFromGlobalSops redacted" "clusterGotmplFromGlobalSops: REDACTED" "$OUTPUT"

# Numbers < 5 digits must be preserved
assert_contains "sopsNumber preserved"   "sopsNumber: 42"    "$OUTPUT"
assert_contains "sopsFloat preserved"    "sopsFloat: 3.14"   "$OUTPUT"
assert_contains "sopsMap.port preserved" "port: 5432"        "$OUTPUT"

# Booleans must be preserved
assert_contains "sopsBool preserved"     "sopsBool: true"    "$OUTPUT"

# Non-tainted values must be preserved
assert_contains "globalOnly preserved"       "globalOnly: fromGlobal"       "$OUTPUT"
assert_contains "clusterOnly preserved"      "clusterOnly: fromCluster"     "$OUTPUT"
assert_contains "deploymentOnly preserved"   "deploymentOnly: fromDeployment" "$OUTPUT"
assert_contains "gotmplFromYaml preserved"   "gotmplFromYaml: fromGlobal"   "$OUTPUT"

# ATLAS infrastructure must not be damaged
assert_contains "deploymentName intact"      "deploymentName: deployment1"   "$OUTPUT"
assert_contains "cluster intact"             "cluster: cluster1"             "$OUTPUT"
assert_not_contains "cwd not damaged"        "cwd: REDACTED"                "$OUTPUT"
assert_not_contains "deploymentPath not damaged" "deploymentPath: REDACTED"  "$OUTPUT"

# ── Test deployment2 on group1/cluster2 (grouped, with SOPS list) ────────────

echo "Testing group1/cluster2/deployment2..."

OUTPUT2=$(ATLAS_REDACT_SECRETS=true helmfile -f "$REPO_ROOT/helmfile.tests.yaml.gotmpl" template \
  --selector "cluster=group1/cluster2,deploymentName=deployment2" \
  --skip-schema-validation \
  2>/dev/null) || {
  echo "FAIL: helmfile template failed for group1/cluster2/deployment2"
  exit 1
}

# Group-level SOPS values
assert_contains "sopsGroup redacted"         "sopsGroup: REDACTED"           "$OUTPUT2"

# SOPS list elements (strings)
assert_not_contains "sopsList itemOne not in output"  "itemOne"              "$OUTPUT2"
assert_not_contains "sopsList itemTwo not in output"  "itemTwo"              "$OUTPUT2"

# Group gotmpl pointer taint
assert_contains "groupGotmplFromGlobalSops redacted" "groupGotmplFromGlobalSops: REDACTED" "$OUTPUT2"

# Non-tainted values preserved
assert_contains "groupOnly preserved"        "groupOnly: fromGroup"          "$OUTPUT2"
assert_contains "clusterOnly preserved"      "clusterOnly: fromCluster"      "$OUTPUT2"

# ── Results ──────────────────────────────────────────────────────────────────

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"

if [ "$FAIL" -gt 0 ]; then
  echo -e "\nFailures:${ERRORS}"
  exit 1
fi

echo "All redaction tests passed."
