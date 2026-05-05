#!/usr/bin/env bats
#
# Scenario: the two-stage redaction-replay pipeline that snapshot-review uses
# to close template-level secret leaks in renders produced by ATLAS versions
# that predate per-release twin-load.
#
# Stage 1 — side-dump: the atlas-redact plugin accepts an optional second
# argv element (a dump path). When the template wires it — which it only
# does if ATLAS_SIDEDUMP_MAP_DIR is set at render time — the plugin writes
# the decoded replacement map to that path as raw JSON. Normal behavior is
# unchanged: the plugin still scrubs the rendered manifests on stdin and
# emits them on stdout.
#
# Stage 2 — replay: the scrub-baseline.sh helper walks a baseline render
# tree (layout = <cluster>/<deployment>/<release>/<chart>/templates/*.yaml)
# and scrubs every release whose matching map exists in the dump directory,
# using the same whole-scalar yq match the plugin uses. Releases with no
# matching map are reported on stdout as `no-map <path>` so the workflow
# can drop them from the diff (can't redact what we don't know about).
#
# These two pieces together let the snapshot-review workflow close leaks
# in old-baseline renders without patching released ATLAS versions.

load 'helpers/render'

REPO_ROOT="${BATS_TEST_DIRNAME}/../../.."
PLUGIN_DIR="${REPO_ROOT}/templates/helm-plugin/atlas-redact"

# --- Stage 1: side-dump during a redacted render ----------------------------
#
# Use the deployment20 fixture (template-level multi-line SOPS + gotmpl-
# derived credentials block) because it exercises both direct SOPS leaves
# AND transitive gotmpl pairs — the most substantial per-release map in
# the suite. If the side-dump path works for this, it works for anything.

setup_file() {
  # Fresh dump dir per bats run. Parent of BATS_RUN_TMPDIR is writable;
  # the plugin creates intermediate dirs itself (mkdir -p).
  export SIDEDUMP_DIR="${BATS_RUN_TMPDIR:-/tmp}/atlas-bats-sidedump"
  export SIDEDUMP_RENDER_DIR="${BATS_RUN_TMPDIR:-/tmp}/atlas-bats-sidedump-render"
  rm -rf "$SIDEDUMP_DIR" "$SIDEDUMP_RENDER_DIR"

  # Use ensure_rendered_redacted's env setup as a baseline, then add the
  # side-dump env. We invoke helmfile directly (not via the helper) so we
  # can scope ATLAS_SIDEDUMP_MAP_DIR just to this render.
  local root default_plugins
  root="$(cd "$REPO_ROOT" && pwd)"
  default_plugins="$(helm env HELM_PLUGINS 2>/dev/null || echo "")"
  ATLAS_REDACT_SECRETS=true \
  ATLAS_SIDEDUMP_MAP_DIR="$SIDEDUMP_DIR" \
  HELM_PLUGINS="${default_plugins:+${default_plugins}:}${root}/templates/helm-plugin" \
    helmfile -f "$root/helmfile.yaml.gotmpl" \
      --state-values-set "atlas.appTemplates=tests/templates" \
      --state-values-set "atlas.deploymentDefinitions=tests/deployments" \
      --state-values-set "atlas.cwd=$root" \
      template --skip-schema-validation \
      --selector "cluster=cluster1,deploymentName=deployment20" \
      --output-dir "$SIDEDUMP_RENDER_DIR" \
      --output-dir-template '{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}' \
      > "${SIDEDUMP_RENDER_DIR}.log" 2>&1 \
    || { echo "sidedump render failed. log:" >&2; cat "${SIDEDUMP_RENDER_DIR}.log" >&2; return 1; }
}

@test "sidedump: map file written at layout path <cluster>/<deployment>/<release>.json" {
  # Layout mirrors --output-dir-template so the scrubber doesn't need a
  # manifest file to look up per-release maps.
  [ -f "$SIDEDUMP_DIR/cluster1/deployment20/app-multiline-tplsops.json" ]
}

@test "sidedump: map file contains the secret → redacted pairs used by the post-renderer" {
  # Presence of a known leaf (awsAccessKeyId → REDACTED) proves end-to-end:
  # twin-load built the diff, the plugin received it, the side-dump
  # persisted the decoded JSON. The full map is also exercised by the
  # multi-line block-scalar assertion below.
  local map_file="$SIDEDUMP_DIR/cluster1/deployment20/app-multiline-tplsops.json"
  run jq -r '."AKIAIOSFODNN7EXAMPLE"' "$map_file"
  [ "$output" = "REDACTED" ]
}

@test "sidedump: multi-line block scalar is a whole-scalar key in the dumped map" {
  # The gotmpl-composed credentials block appears as one key in the diff —
  # this is what enables whole-scalar replacement in the scrubber.
  local map_file="$SIDEDUMP_DIR/cluster1/deployment20/app-multiline-tplsops.json"
  run jq -r 'keys[] | select(contains("aws_access_key_id = AKIAIOSFODNN7EXAMPLE"))' "$map_file"
  [ -n "$output" ]
}

@test "sidedump: normal render (no env var) produces no dump dir" {
  # Guards against the side-dump path accidentally activating in
  # production. Uses a fresh scratch dir so we know no prior test leaked.
  local scratch="${BATS_TEST_TMPDIR}/no-sidedump"
  local root default_plugins
  root="$(cd "$REPO_ROOT" && pwd)"
  default_plugins="$(helm env HELM_PLUGINS 2>/dev/null || echo "")"
  ATLAS_REDACT_SECRETS=true \
  HELM_PLUGINS="${default_plugins:+${default_plugins}:}${root}/templates/helm-plugin" \
    helmfile -f "$root/helmfile.yaml.gotmpl" \
      --state-values-set "atlas.appTemplates=tests/templates" \
      --state-values-set "atlas.deploymentDefinitions=tests/deployments" \
      --state-values-set "atlas.cwd=$root" \
      template --skip-schema-validation \
      --selector "cluster=cluster1,deploymentName=deployment20" \
      --output-dir "${scratch}/render" \
      --output-dir-template '{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}' \
      > "${scratch}.log" 2>&1
  # Nothing under the expected dump path location.
  [ ! -d "${scratch}/sidedump" ]
}

# --- Stage 2: scrub-baseline.sh replays a captured map ----------------------
#
# Synthetic baseline: a YAML doc with an unredacted secret scalar in the
# shape an old ATLAS would produce. We pair it with a tiny handcrafted map
# and verify the scrubber replaces the scalar and emits the right status
# line. Keeping it synthetic — rather than spinning up an old ATLAS —
# makes the test fast and hermetic; the plugin's end-to-end path above
# already covers the map-capture half.

@test "scrubber: replaces a whole-scalar match and emits 'scrubbed <release>'" {
  local scratch="${BATS_TEST_TMPDIR}/scrub-happy"
  local baseline="${scratch}/baseline"
  local mapdir="${scratch}/maps"
  local release_dir="${baseline}/cluster1/dep/rel/chart/templates"
  mkdir -p "$release_dir" "${mapdir}/cluster1/dep"

  # Leaky manifest — cleartext secret in a stringData field.
  cat > "$release_dir/secret.yaml" <<'YAML'
apiVersion: v1
kind: Secret
metadata:
  name: leaky
stringData:
  token: s3cretvalue
YAML

  # Matching map: single whole-scalar pair.
  cat > "${mapdir}/cluster1/dep/rel.json" <<'JSON'
{"s3cretvalue":"REDACTED"}
JSON

  run bash "${PLUGIN_DIR}/scrub-baseline.sh" "$baseline" "$mapdir"
  [ "$status" -eq 0 ]
  [[ "$output" == *"scrubbed cluster1/dep/rel"* ]]

  # Baseline file rewritten in place with the replacement applied.
  run yq '.stringData.token' "$release_dir/secret.yaml"
  [ "$output" = "REDACTED" ]
}

@test "scrubber: release with no matching map is reported 'no-map' and left untouched" {
  local scratch="${BATS_TEST_TMPDIR}/scrub-nomap"
  local baseline="${scratch}/baseline"
  local mapdir="${scratch}/maps"
  local release_dir="${baseline}/cluster1/dep/orphan/chart/templates"
  mkdir -p "$release_dir" "$mapdir"

  cat > "$release_dir/secret.yaml" <<'YAML'
apiVersion: v1
kind: Secret
metadata:
  name: orphan
stringData:
  token: s3cretvalue
YAML

  run bash "${PLUGIN_DIR}/scrub-baseline.sh" "$baseline" "$mapdir"
  [ "$status" -eq 0 ]
  [[ "$output" == *"no-map cluster1/dep/orphan"* ]]

  # Baseline untouched — whatever was leaky stays leaky. The workflow is
  # responsible for dropping no-map releases from the diff.
  run yq '.stringData.token' "$release_dir/secret.yaml"
  [ "$output" = "s3cretvalue" ]
}

@test "scrubber: replays a map captured by stage 1 against a synthetic leak" {
  # End-to-end integration: use the real side-dumped map from setup_file
  # and confirm it scrubs a synthetic manifest containing the same secret.
  # This locks in that stage 1 and stage 2 share a compatible map format.
  local scratch="${BATS_TEST_TMPDIR}/scrub-e2e"
  local baseline="${scratch}/baseline"
  local mapdir="${scratch}/maps"
  local release_dir="${baseline}/cluster1/deployment20/app-multiline-tplsops/chart1/templates"
  mkdir -p "$release_dir" "${mapdir}/cluster1/deployment20"

  # Copy the captured map into the synthetic baseline layout.
  cp "$SIDEDUMP_DIR/cluster1/deployment20/app-multiline-tplsops.json" \
     "${mapdir}/cluster1/deployment20/app-multiline-tplsops.json"

  # Synthetic "leaky baseline" manifest containing the cleartext secret
  # shape an older ATLAS would have emitted.
  cat > "$release_dir/secret.yaml" <<'YAML'
apiVersion: v1
kind: Secret
metadata:
  name: aws-creds
stringData:
  accessKeyId: AKIAIOSFODNN7EXAMPLE
YAML

  run bash "${PLUGIN_DIR}/scrub-baseline.sh" "$baseline" "$mapdir"
  [ "$status" -eq 0 ]
  [[ "$output" == *"scrubbed cluster1/deployment20/app-multiline-tplsops"* ]]

  run yq '.stringData.accessKeyId' "$release_dir/secret.yaml"
  [ "$output" = "REDACTED" ]
}
