# render.bash — bulk-render all ATLAS test deployments once per bats run.
#
# `helmfile template` already renders every sub-helmfile in parallel internally,
# so we invoke it exactly once (no selector) and use --output-dir-template to
# scatter each release's manifests into a structured directory tree keyed by
# (cluster, deploymentName, release). Assertions then become plain file reads.
#
# Output layout (under $RENDER_DIR):
#   <cluster>/<deploymentName>/<release>/chart1/templates/configmap.yaml
#   e.g. cluster1/deployment1/app1/chart1/templates/configmap.yaml
#        group1/cluster2/deployment8/app-multi-first/chart1/templates/configmap.yaml
#
# Usage in a .bats file:
#   load 'helpers/render'
#   setup_file() { ensure_rendered; }
#   @test "something" {
#     run get_path cluster1 deployment1 app1 .globalOnly
#     [ "$output" = "fromGlobal" ]
#   }

# Roots of the rendered-output trees. BATS_RUN_TMPDIR is fresh per `bats` run,
# so both caches are rebuilt on every invocation.
#   RENDER_DIR          — default render (plain values)
#   RENDER_DIR_REDACTED — redacted render (ATLAS_REDACT_SECRETS=true, runs the
#                         atlas-redact helm post-renderer to scrub SOPS-tainted
#                         values while preserving non-secret ones)
RENDER_DIR="${BATS_RUN_TMPDIR:-/tmp}/atlas-bats-render"
RENDER_DIR_REDACTED="${BATS_RUN_TMPDIR:-/tmp}/atlas-bats-render-redacted"

# _repo_root returns the ATLAS repo root based on this helper's location:
# <root>/tests/bats/helpers/render.bash → go up three directories.
_repo_root() {
  cd "${BATS_TEST_DIRNAME}/../.." && pwd
}

# ensure_rendered performs the bulk render exactly once per bats run.
# Call from setup_file(); subsequent calls are no-ops.
ensure_rendered() {
  [[ -d "$RENDER_DIR" ]] && return 0
  local root
  root="$(_repo_root)"
  # Dogfoods tests/helmfile.yaml.gotmpl — the test entry point that pre-wires
  # atlas.{cwd,appTemplates,deploymentDefinitions} and hands off to the real
  # ATLAS entry at the repo root. Same code path consumers exercise; the test
  # entry just spares us the three --state-values-set flags.
  #
  # --skip-schema-validation: no CRD dependency needed for value assertions.
  # --output-dir-template: lays out manifests by cluster/deployment/release so
  # tests can look up a single instance by path rather than filtering YAML.
  helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    template --skip-schema-validation \
    --output-dir "$RENDER_DIR" \
    --output-dir-template '{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}' \
    > "${RENDER_DIR}.log" 2>&1 \
    || { echo "helmfile template failed. log:" >&2; \
         cat "${RENDER_DIR}.log" >&2; return 1; }
}

# ensure_rendered_redacted performs the redacted render once per bats run.
# Turns on ATLAS_REDACT_SECRETS, which causes the atlas-redact helm
# post-renderer to run: SOPS-tainted string values become "REDACTED", while
# short numerics, booleans, and non-tainted keys pass through untouched. The
# helm plugin is enabled via HELM_PLUGINS, appended only for this invocation.
# Call from setup_file() in any .bats file that needs redacted output.
ensure_rendered_redacted() {
  [[ -d "$RENDER_DIR_REDACTED" ]] && return 0
  local root default_plugins
  root="$(_repo_root)"
  # Preserve any HELM_PLUGINS the user already has and append the atlas-redact
  # plugin dir so helm can load it as a post-renderer.
  default_plugins="$(helm env HELM_PLUGINS 2>/dev/null || echo "")"
  ATLAS_REDACT_SECRETS=true \
  HELM_PLUGINS="${default_plugins:+${default_plugins}:}${root}/templates/helm-plugin" \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
      template --skip-schema-validation \
      --output-dir "$RENDER_DIR_REDACTED" \
      --output-dir-template '{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}' \
      > "${RENDER_DIR_REDACTED}.log" 2>&1 \
    || { echo "redacted helmfile template failed. log:" >&2; \
         cat "${RENDER_DIR_REDACTED}.log" >&2; return 1; }
}

# _release_dir returns the per-release output directory. The output-dir-template
# keys by (cluster, deployment, release), so this directory is always unique —
# no cross-release collisions even when multiple clusters share a release name.
_release_dir() {
  local cluster="$1" deployment="$2" instance="$3"
  echo "${RENDER_DIR}/${cluster}/${deployment}/${instance}"
}

# _release_dir_redacted is the redacted-render counterpart of _release_dir.
_release_dir_redacted() {
  local cluster="$1" deployment="$2" instance="$3"
  echo "${RENDER_DIR_REDACTED}/${cluster}/${deployment}/${instance}"
}

# values_for emits the resolved values block for a single deployment+instance.
# We scan all yaml files in the release dir and filter by kind+metadata.name
# rather than opening a fixed filename, because helmfile rewrites the output
# path when patches or transformers are applied:
#   - no patches     → chart1/templates/configmap.yaml
#   - with patches   → chart1/templates/patched_resources.yaml (multi-doc)
# The chart serializes .Values to .data.values as a parsed YAML map (see
# tests/charts/chart1/templates/configmap.yaml: toYaml | nindent 4).
#
# Args: $1=cluster   $2=deployment   $3=instance
# Emits: parsed values as YAML on stdout.
values_for() {
  local dir cm_name
  dir="$(_release_dir "$1" "$2" "$3")"
  cm_name="${3}-chart1"
  yq "select(.kind == \"ConfigMap\" and .metadata.name == \"${cm_name}\") | .data.values" \
    "$dir"/chart1/templates/*.yaml
}

# get_path extracts a single yq-path value from a deployment+instance's values.
# Use via `run` for bats assertions.
#
# Args: $1=cluster   $2=deployment   $3=instance   $4=yq path (e.g. .globalOnly)
get_path() {
  values_for "$1" "$2" "$3" | yq "$4"
}

# values_for_redacted / get_path_redacted are the redacted-render counterparts
# of values_for / get_path. They read from the ATLAS_REDACT_SECRETS render
# tree, so assertions can check both "secrets became REDACTED" and
# "non-secrets were preserved unchanged" from the same rendered output.
values_for_redacted() {
  local dir cm_name
  dir="$(_release_dir_redacted "$1" "$2" "$3")"
  cm_name="${3}-chart1"
  yq "select(.kind == \"ConfigMap\" and .metadata.name == \"${cm_name}\") | .data.values" \
    "$dir"/chart1/templates/*.yaml
}

get_path_redacted() {
  values_for_redacted "$1" "$2" "$3" | yq "$4"
}

# instance_rendered returns success iff a (cluster, deployment, instance) tuple
# produced any output — i.e. the release was scheduled and rendered. Checks the
# release dir rather than a specific file, so it works regardless of whether
# patches/transformers changed the output filename.
instance_rendered() {
  local dir
  dir="$(_release_dir "$1" "$2" "$3")"
  [[ -d "$dir" ]] && compgen -G "$dir/chart1/templates/*.yaml" > /dev/null
}

# instance_rendered_any is the chart-name-agnostic variant of instance_rendered:
# it succeeds if the release produced any yaml anywhere under its output dir.
# Use this for releases whose chart isn't named chart1 (e.g. releases that
# point chart: at a sibling directory with a kustomization or plain manifests —
# helmfile wraps those via chartify into a chart named after the release).
instance_rendered_any() {
  local dir
  dir="$(_release_dir "$1" "$2" "$3")"
  [[ -d "$dir" ]] && compgen -G "$dir/*/templates/*.yaml" > /dev/null
}

# render_contains succeeds if any yaml under the release's output dir contains
# the given literal pattern. Use for releases whose output isn't a chart1
# ConfigMap (the values_for / get_path helpers are chart1-specific).
# Args: $1=cluster $2=deployment $3=instance $4=literal grep pattern
render_contains() {
  local dir
  dir="$(_release_dir "$1" "$2" "$3")"
  [[ -d "$dir" ]] || return 1
  grep -rqF -- "$4" "$dir"
}

# release_labels emits the merged labels map for a single release. Unlike
# values_for (which reads rendered manifests), this queries `helmfile build`
# because instance-level labels live in the helmfile release state, not in the
# rendered Kubernetes resources — helm doesn't inject commonLabels/release
# labels into manifest metadata during `helmfile template`.
#
# The yq filter narrows by cluster + deploymentName labels in addition to the
# release name: helmfile versions without helmfile/helmfile#2545 do not
# shortcut sub-helmfile parsing when `--selector` is given, so `helmfile build`
# emits every sub-helmfile's releases. Many fixtures share a release name
# (e.g. "app1") across multiple (cluster, deployment) tuples, so we'd get a
# multi-doc stream that hides the target. Matching on cluster + deploymentName
# picks exactly the intended release regardless of helmfile version.
#
# Args: $1=cluster   $2=deployment   $3=instance
# Emits: the labels map as YAML on stdout.
release_labels() {
  local cluster="$1" deployment="$2" instance="$3" root
  root="$(_repo_root)"
  helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    build --selector "cluster=${cluster},deploymentName=${deployment}" 2>/dev/null |
    yq "select(.releases != null) | .releases[] | select(.name == \"${instance}\" and .labels.cluster == \"${cluster}\" and .labels.deploymentName == \"${deployment}\") | .labels"
}
