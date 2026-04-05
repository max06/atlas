# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ATLAS (Application Topology Layer Across Systems) is a pre-configured Helmfile system that automatically discovers Kubernetes clusters in GitOps repositories and assigns applications using hierarchical inheritance: **Global → Group → Cluster**. It is a work-in-progress and not production-ready.

The codebase is primarily Go Templates (`.gotmpl`), Helm Templates (`.tpl`), and YAML. There is no traditional build step — Helmfile is the orchestrator.

## Common Commands

```bash
# Run bats integration tests (value inheritance, SOPS, instances, multi-template)
bats tests/bats/

# Render a single deployment for debugging (uses the same entry point as consumer repos,
# with state-values pointing at the tests/ fixtures)
helmfile -f helmfile.yaml.gotmpl \
  --state-values-set atlas.appTemplates=tests/templates \
  --state-values-set atlas.deploymentDefinitions=tests/deployments \
  --state-values-set atlas.cwd=$(pwd) \
  template --skip-schema-validation --selector cluster=cluster1,deploymentName=deployment1

# View value loading trace for debugging
helmfile -f helmfile.yaml.gotmpl \
  --state-values-set atlas.appTemplates=tests/templates \
  --state-values-set atlas.deploymentDefinitions=tests/deployments \
  --state-values-set atlas.cwd=$(pwd) \
  build --debug --selector cluster=cluster1,deploymentName=deployment1
```

## Architecture

### Entry Points

- **helmfile.yaml.gotmpl** — Single entry point for both consumer GitOps repositories and ATLAS's own tests; includes `templates/helmfile.all.yaml.gotmpl` and passes `atlas` config values from the caller. Tests dogfood this entry point by passing state-values that point at `tests/templates/` and `tests/deployments/` (mocked fixtures) plus a testable Helm chart at `tests/charts/chart1/` that serializes all resolved values into a ConfigMap for inspection. Assertions live in `tests/bats/` and run against the rendered ConfigMaps to validate inheritance, SOPS decryption, named instances, and multi-template deployments.

### Core Template Pipeline

1. **templates/helmfile.all.yaml.gotmpl** — Discovers clusters by scanning for directories containing `/apps` subdirectories, filters to leaf clusters, then collects deployments at three hierarchy levels (global, group, cluster). Outputs helmfile entries pointing to the single deployment renderer.

2. **templates/helmfile.single.yaml.gotmpl** — Renders a single deployment by loading values from multiple sources in priority order (global → group → cluster → deployment level), decrypting SOPS values, and rendering the deployment's `deployment.yaml` as a Go template.

3. **templates/_functions.tpl** — Reusable helper library providing: list override application (`atlas.applyListOverride`), path conversion (`convertPaths`), and custom glob pattern matching (`glob`, `globIterative`, `globRecursive`).

### Deployment Hierarchy

Deployments follow a directory convention where values cascade through inheritance:

```
deployments/
  global.values.yaml              # Applied to ALL clusters
  global.values.sops.yaml         # Encrypted global values
  apps/<name>/deployment.yaml     # Global-level deployment
  <group>/
    apps/<name>/deployment.yaml   # Group-level deployment
    <cluster>/
      apps/<name>/deployment.yaml # Cluster-specific deployment
```

A `deployment.yaml` references an app template and provides values:
```yaml
apps:
  - template: app-name
    namespace: default
    values:
      - key: value
```

### App Templates

Located in a templates directory (production: configurable, tests: `tests/templates/`). Each template is a `helmfile.yaml.gotmpl` defining Helm releases.

### Secrets

SOPS is used for per-key encryption in `*.values.sops.yaml` files. Decryption uses `fetchSecretValue` with the `ref+sops://` protocol — whole-file decryption (no `#key` fragment) returns the full decrypted YAML as a string, preserving all value types. Do not use external CLI tools via `exec` for decryption.

### Atlas Config Object

Templates receive an `atlas` values object containing:
- `cwd` — Absolute working directory path
- `deploymentDefinitions` — Path to deployments directory
- `appTemplates` — Path to application templates
- `redactSecrets` — Enable type-aware secret redaction
- `variant` — Variant label applied to every rendered resource (defaults to `default`)
- `deployment.cluster` / `deployment.deploymentName` / `deployment.deploymentPath` — Current deployment context (set by the pipeline)

## Development Rules

### Making Changes to Templates
- Make small, incremental changes — fix one issue at a time, not everything at once.
- Validate every change by running the tests.
- If no test covers your change, create a new test case before or alongside the change.
- Do **not** modify or remove existing tests. If a test seems wrong, flag it for review rather than changing it.
- If tests keep failing after a fix attempt, write a note and move on instead of retrying different solutions.
- After all fixes are done, review for code duplication, duplicate variables, missing comments, and unnecessary steps.
- Do not refactor working code aggressively — refactoring is a separate step done before committing.

### Template Readability
- Templates are read and modified by humans. Use descriptive variable names (e.g., `$loadedValues` not `$vals`, `$deploymentDefinition` not `$dep`).
- Add extensive documentation in the form of code comments, especially for non-obvious logic.

### No External Tooling in Templates
- Do not rely on external CLI tools via `exec` (e.g., `exec "sops"`). Templates may run in environments where such tools are not available.
- Use helmfile's built-in functions (`fetchSecretValue`, `readFile`, `tpl`, etc.) for all template logic.
