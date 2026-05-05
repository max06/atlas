#!/usr/bin/env bats
#
# Scenario: ATLAS's secret-redaction post-renderer (the atlas-redact helm
# plugin) rewrites SOPS-tainted values in the rendered output while preserving
# non-tainted keys and short numerics/booleans.
#
# Redaction rules:
#   - Tainted strings              → "REDACTED"
#   - Tainted numbers < 5 digits   → preserved as-is (too short to leak info)
#   - Tainted booleans             → preserved (only two values anyway)
#   - Pointer-tainted values       → a gotmpl referencing a SOPS key inherits
#                                    the taint and is redacted
#   - Non-tainted values           → preserved unchanged across ALL sources
#     (chart defaults, template-include, template-defaults, hierarchy yaml/gotmpl)
#
# Ported from tests/test-redaction.sh plus added preservation assertions for
# every source type not previously covered (chart-defaults, template-include,
# template-defaults, group/cluster/deployment gotmpl yaml-derived keys).

load 'helpers/render'

setup_file() { ensure_rendered_redacted; }

# ============================================================================
# Standalone cluster (cluster1/deployment1/app1) — global + cluster + deployment
# ============================================================================

# --- SOPS string values → REDACTED -----------------------------------------

@test "redact d1: sopsGlobal (global string) redacted" {
  run get_path_redacted cluster1 deployment1 app1 .sopsGlobal
  [ "$output" = "REDACTED" ]
}

@test "redact d1: sopsString (global string) redacted" {
  # "hello-from-sops" → segment-preserving redaction around dashes
  run get_path_redacted cluster1 deployment1 app1 .sopsString
  [ "$output" = "REDAC-REDA-REDA" ]
}

@test "redact d1: sopsCluster (cluster string) redacted" {
  run get_path_redacted cluster1 deployment1 app1 .sopsCluster
  [ "$output" = "REDACTED" ]
}

@test "redact d1: sopsDeployment (deployment string) redacted" {
  run get_path_redacted cluster1 deployment1 app1 .sopsDeployment
  [ "$output" = "REDACTED" ]
}

@test "redact d1: sopsOverride (multi-level string) redacted" {
  run get_path_redacted cluster1 deployment1 app1 .sopsOverride
  [ "$output" = "REDACTED" ]
}

@test "redact d1: sopsMap.username redacted" {
  # "admin" (5 alphanumeric chars) → first 5 of "REDACTED"
  run get_path_redacted cluster1 deployment1 app1 .sopsMap.username
  [ "$output" = "REDAC" ]
}

@test "redact d1: sopsMap.password redacted" {
  # "s3cret!" (6 alphanumeric + "!") → "REDACT!" (6 from REDACTED, "!" preserved)
  run get_path_redacted cluster1 deployment1 app1 .sopsMap.password
  [ "$output" = "REDACT!" ]
}

@test "redact d1: sopsNested.secretKey redacted" {
  run get_path_redacted cluster1 deployment1 app1 .sopsNested.secretKey
  [ "$output" = "REDACTED" ]
}

@test "redact d1: sopsNested.deep.verySecret redacted" {
  run get_path_redacted cluster1 deployment1 app1 .sopsNested.deep.verySecret
  [ "$output" = "REDACTED" ]
}

# --- Pointer-tainted gotmpl values → REDACTED -----------------------------

@test "redact d1: gotmplFromSops (sops-derived gotmpl) redacted" {
  run get_path_redacted cluster1 deployment1 app1 .gotmplFromSops
  [ "$output" = "REDACTED" ]
}

@test "redact d1: clusterGotmplFromGlobalSops (cluster gotmpl, sops-derived) redacted" {
  run get_path_redacted cluster1 deployment1 app1 .clusterGotmplFromGlobalSops
  [ "$output" = "REDACTED" ]
}

# --- Short numbers → preserved (under 5 digits) ---------------------------

@test "redact d1: sopsNumber 42 preserved (short int)" {
  run get_path_redacted cluster1 deployment1 app1 .sopsNumber
  [ "$output" = "42" ]
}

@test "redact d1: sopsFloat 3.14 preserved" {
  run get_path_redacted cluster1 deployment1 app1 .sopsFloat
  [ "$output" = "3.14" ]
}

@test "redact d1: sopsMap.port 5432 preserved (4-digit int)" {
  run get_path_redacted cluster1 deployment1 app1 .sopsMap.port
  [ "$output" = "5432" ]
}

# --- Booleans → preserved --------------------------------------------------

@test "redact d1: sopsBool true preserved" {
  run get_path_redacted cluster1 deployment1 app1 .sopsBool
  [ "$output" = "true" ]
}

# --- Preservation of non-tainted values from every source -----------------

@test "redact d1: chart default aChartValue preserved" {
  run get_path_redacted cluster1 deployment1 app1 .aChartValue
  [ "$output" = "on default" ]
}

@test "redact d1: template-include templateInclude preserved" {
  run get_path_redacted cluster1 deployment1 app1 .templateInclude
  [ "$output" = "fromInclude" ]
}

@test "redact d1: template-include templateOverride preserved" {
  run get_path_redacted cluster1 deployment1 app1 .templateOverride
  [ "$output" = "default" ]
}

@test "redact d1: template-defaults templateDefault preserved" {
  run get_path_redacted cluster1 deployment1 app1 .templateDefault
  [ "$output" = "fromDefault" ]
}

@test "redact d1: global yaml globalOnly preserved" {
  run get_path_redacted cluster1 deployment1 app1 .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "redact d1: global gotmpl yaml-derived gotmplFromYaml preserved" {
  run get_path_redacted cluster1 deployment1 app1 .gotmplFromYaml
  [ "$output" = "fromGlobal" ]
}

@test "redact d1: cluster yaml clusterOnly preserved" {
  run get_path_redacted cluster1 deployment1 app1 .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "redact d1: cluster gotmpl yaml-derived clusterGotmplFromGlobalYaml preserved" {
  run get_path_redacted cluster1 deployment1 app1 .clusterGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "redact d1: deployment yaml deploymentOnly preserved" {
  run get_path_redacted cluster1 deployment1 app1 .deploymentOnly
  [ "$output" = "fromDeployment" ]
}

@test "redact d1: deployment gotmpl yaml-derived deployGotmplFromClusterYaml preserved" {
  run get_path_redacted cluster1 deployment1 app1 .deployGotmplFromClusterYaml
  [ "$output" = "fromCluster" ]
}

# --- ATLAS infrastructure keys → NOT damaged by redaction -----------------

@test "redact d1: atlas.deployment.cluster not damaged" {
  run get_path_redacted cluster1 deployment1 app1 .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
}

@test "redact d1: atlas.deployment.deploymentName not damaged" {
  run get_path_redacted cluster1 deployment1 app1 .atlas.deployment.deploymentName
  [ "$output" = "deployment1" ]
}

@test "redact d1: atlas.cwd not redacted" {
  run get_path_redacted cluster1 deployment1 app1 .atlas.cwd
  [ "$output" != "REDACTED" ]
  [ -n "$output" ]
}

@test "redact d1: atlas.deployment.deploymentPath not damaged" {
  run get_path_redacted cluster1 deployment1 app1 .atlas.deployment.deploymentPath
  [ "$output" != "REDACTED" ]
  [[ "$output" == *deployment1* ]]
}

# ============================================================================
# Grouped cluster (group1/cluster2/deployment2/app1) — group layer included
# ============================================================================

@test "redact d2: sopsGroup (group string) redacted" {
  run get_path_redacted group1/cluster2 deployment2 app1 .sopsGroup
  [ "$output" = "REDACTED" ]
}

@test "redact d2: sopsList[0] (group list) redacted" {
  # "itemOne" (7 alphanumeric chars) → first 7 of "REDACTED"
  run get_path_redacted group1/cluster2 deployment2 app1 '.sopsList[0]'
  [ "$output" = "REDACTE" ]
}

@test "redact d2: sopsList[1] redacted" {
  # "itemTwo" (7 alphanumeric chars) → first 7 of "REDACTED"
  run get_path_redacted group1/cluster2 deployment2 app1 '.sopsList[1]'
  [ "$output" = "REDACTE" ]
}

@test "redact d2: sopsBoolTrue preserved" {
  run get_path_redacted group1/cluster2 deployment2 app1 .sopsBoolTrue
  [ "$output" = "true" ]
}

@test "redact d2: sopsBoolFalse preserved" {
  run get_path_redacted group1/cluster2 deployment2 app1 .sopsBoolFalse
  [ "$output" = "false" ]
}

@test "redact d2: groupGotmplFromGlobalSops (group gotmpl, sops-derived) redacted" {
  run get_path_redacted group1/cluster2 deployment2 app1 .groupGotmplFromGlobalSops
  [ "$output" = "REDACTED" ]
}

# --- Preservation in grouped render ---------------------------------------

@test "redact d2: group yaml groupOnly preserved" {
  run get_path_redacted group1/cluster2 deployment2 app1 .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "redact d2: group gotmpl yaml-derived groupGotmplFromGlobalYaml preserved" {
  run get_path_redacted group1/cluster2 deployment2 app1 .groupGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "redact d2: cluster gotmpl yaml-derived clusterGotmplFromGroupYaml preserved" {
  run get_path_redacted group1/cluster2 deployment2 app1 .clusterGotmplFromGroupYaml
  [ "$output" = "fromGroup" ]
}

@test "redact d2: cluster gotmpl chained clusterGotmplFromGroupGotmpl preserved" {
  run get_path_redacted group1/cluster2 deployment2 app1 .clusterGotmplFromGroupGotmpl
  [ "$output" = "fromGlobal" ]
}

@test "redact d2: deployment gotmpl yaml-derived deployGotmplFromGroupYaml preserved" {
  run get_path_redacted group1/cluster2 deployment2 app1 .deployGotmplFromGroupYaml
  [ "$output" = "fromGroup" ]
}

# ============================================================================
# Multi-release (deployment8/app-multi-first) — template-defaults coverage
# ============================================================================

@test "redact d8/first: template-defaults multiRelease preserved" {
  run get_path_redacted group1/cluster2 deployment8 app-multi-first .multiRelease
  [ "$output" = "first" ]
}

@test "redact d8/second: template-defaults multiRelease preserved" {
  run get_path_redacted group1/cluster2 deployment8 app-multi-second .multiRelease
  [ "$output" = "second" ]
}

# ============================================================================
# Multi-line values (deployment13) — block-scalar-aware redaction
# ============================================================================

@test "redact d13: multi-line SOPS secret replaced, preserving block shape" {
  # Multi-line values get structure-preserving redaction per the atlas.redact
  # rules: each alphanumeric run becomes REDACTED[:min(len,8)], delimiters
  # (dashes, spaces, newlines) kept in place. Original content (key bytes,
  # real PEM markers) must not appear anywhere in the redacted string.
  run get_path_redacted cluster1 deployment13 app-novals .multilineSecret
  [[ "$output" != *"BEGIN FAKE"* ]]
  [[ "$output" != *"MIIE"* ]]
  [[ "$output" != *"testkey"* ]]
  [[ "$output" == *"REDACTED"* ]]   # at least one full REDACTED run
  [[ "$output" == *"-----"* ]]       # dashes preserved
  [[ "$output" == *$'\n'* ]]         # newlines preserved
}

@test "redact d13: multi-line plain-yaml value preserved (not tainted)" {
  run get_path_redacted cluster1 deployment13 app-novals .multilinePlain
  [[ "$output" == *"first line from plain yaml"* ]]
  [[ "$output" == *"third line"* ]]
}

@test "redact d13: multi-line gotmpl value preserved (not tainted)" {
  # gotmpl file here only references non-SOPS values (globalOnly, clusterOnly),
  # so the resulting multi-line string inherits no pointer taint.
  run get_path_redacted cluster1 deployment13 app-novals .multilineGotmpl
  [[ "$output" == *"line one renders fromGlobal"* ]]
  [[ "$output" == *"line three is static"* ]]
}

# --- Template-level SOPS redaction (d17: app-tplsops) -------------------------

@test "redact d17: template SOPS scalar (tplSopsSecret) redacted" {
  # "fromTemplateSops" (16 alphanumeric) → first 8 of "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsSecret
  [ "$output" = "REDACTED" ]
}

@test "redact d17: template SOPS override value redacted" {
  # "fromSops" (8 alphanumeric) → "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsOverride
  [ "$output" = "REDACTED" ]
}

@test "redact d17: template SOPS nested map.username redacted" {
  # "tplUser" (7 alphanumeric) → first 7 of "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsMap.username
  [ "$output" = "REDACTE" ]
}

@test "redact d17: template SOPS nested map.token redacted" {
  # "s3cret" (6 alphanumeric) → first 6 of "REDACTED"
  run get_path_redacted cluster1 deployment17 app-tplsops .tplSopsMap.token
  [ "$output" = "REDACT" ]
}

@test "redact d17: plain template-include value preserved" {
  # "fromInclude" is not SOPS-derived — must not be rewritten.
  run get_path_redacted cluster1 deployment17 app-tplsops .tplInclude
  [ "$output" = "fromInclude" ]
}

# --- Template-level SOPS in values: list (d18: app-tplvalsops) ----------------

@test "redact d18: inlined SOPS scalar (tplValsSecret) redacted" {
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsSecret
  [ "$output" = "REDACTED" ]
}

@test "redact d18: inlined SOPS nested map.apiKey redacted" {
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsNested.apiKey
  [ "$output" = "RE-RED-REDACTED" ]
}

@test "redact d18: inlined SOPS nested map.token redacted" {
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsNested.token
  [ "$output" = "RED-REDAC-RED" ]
}

@test "redact d18: plain inline value preserved" {
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsInline
  [ "$output" = "fromInline" ]
}

@test "redact d18: SOPS value overridden by inline stays as inline (not redacted)" {
  run get_path_redacted cluster1 deployment18 app-tplvalsops .tplValsOverride
  [ "$output" = "inline" ]
}

# --- Transitive taint through derived gotmpl (d19: app-crossref) --------------

@test "redact d19: direct SOPS scalar clusterdomain redacted" {
  run get_path_redacted cluster1 deployment19 app-crossref .clusterdomain
  [ "$output" = "RED.RED" ]
}

@test "redact d19: direct SOPS scalar apiToken redacted" {
  run get_path_redacted cluster1 deployment19 app-crossref .apiToken
  [ "$output" = "REDACT" ]
}

@test "redact d19: gotmpl derivative of a SOPS key is redacted (transitive)" {
  run get_path_redacted cluster1 deployment19 app-crossref .domainFromSops
  [ "$output" = "app.RED.RED" ]
}

@test "redact d19: gotmpl mixing hierarchy + template SOPS is redacted (transitive)" {
  run get_path_redacted cluster1 deployment19 app-crossref .mixedFromHierarchyAndSops
  [ "$output" = "fromGlobal-RED.RED" ]
}

@test "redact d19: non-SOPS gotmpl derivative stays as-is" {
  run get_path_redacted cluster1 deployment19 app-crossref .hostFromPlain
  [ "$output" = "crossref.us-east-1.local" ]
}

@test "redact d19: plain inline keys preserved" {
  run get_path_redacted cluster1 deployment19 app-crossref .region
  [ "$output" = "us-east-1" ]
  run get_path_redacted cluster1 deployment19 app-crossref .appName
  [ "$output" = "crossref" ]
}

# --- Multi-line template SOPS (d20: app-multiline-tplsops) --------------------

@test "redact d20: template multi-line SOPS block redacted structurally" {
  run get_path_redacted cluster1 deployment20 app-multiline-tplsops .multilineTplSecret
  [[ "$output" != *"BEGIN FAKE"* ]]
  [[ "$output" != *"MIIEvQ"* ]]
  [[ "$output" != *"tplkey"* ]]
  [[ "$output" == *"REDACTED"* ]]
  [[ "$output" == *"-----"* ]]
  [[ "$output" == *$'\n'* ]]
}

@test "redact d20: template SOPS leaf awsAccessKeyId redacted" {
  run get_path_redacted cluster1 deployment20 app-multiline-tplsops .awsAccessKeyId
  [ "$output" = "REDACTED" ]
}

@test "redact d20: template SOPS leaf awsSecretAccessKey redacted" {
  run get_path_redacted cluster1 deployment20 app-multiline-tplsops .awsSecretAccessKey
  [ "$output" = "REDACTED/REDACTE/REDACTED" ]
}

@test "redact d20: gotmpl-composed credentials block has no plain secret bytes" {
  run get_path_redacted cluster1 deployment20 app-multiline-tplsops .awsCredentials
  [[ "$output" != *"AKIAIOSFODNN7EXAMPLE"* ]]
  [[ "$output" != *"wJalrXUtnFEMI"* ]]
  [[ "$output" != *"EXAMPLEKEY"* ]]
}

@test "redact d20: gotmpl-composed credentials preserves literals + swaps interpolations" {
  run get_path_redacted cluster1 deployment20 app-multiline-tplsops .awsCredentials
  [[ "$output" == *"[default]"* ]]
  [[ "$output" == *"aws_access_key_id = REDACTED"* ]]
  [[ "$output" == *"aws_secret_access_key = REDACTED/REDACTE/REDACTED"* ]]
}

# --- Chart defaults survive redaction -----------------------------------------

@test "redact: chart default aChartValue survives redaction (not tainted)" {
  run get_path_redacted cluster1 deployment1 app1 .aChartValue
  [ "$output" = "on default" ]
}
