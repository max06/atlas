# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ATLAS (Application Topology Layer Across Systems) is a pre-configured Helmfile system that automatically discovers Kubernetes clusters in GitOps repositories and assigns applications using hierarchical inheritance: **Global → Group → Cluster**. It is a work-in-progress and not production-ready.

The codebase is primarily Go Templates (`.gotmpl`), Helm Templates (`.tpl`), and YAML. There is no traditional build step — Helmfile is the orchestrator.

## Common Commands

```bash
# Run ATLAS unit tests (verifies value inheritance logic using mocked templates/deployments)
# Requires SOPS_AGE_KEY_FILE to be set for sops decryption tests
SOPS_AGE_KEY_FILE=tests/sops-secret.txt helmfile -f helmfile.tests.yaml.gotmpl unittest
```

## Architecture

### Entry Points

- **helmfile.yaml.gotmpl** — Main entry point for GitOps repositories consuming ATLAS; includes `templates/helmfile.all.yaml.gotmpl` and passes `atlas` config values.
- **helmfile.tests.yaml.gotmpl** — Test entry point for verifying ATLAS's own logic; uses mocked templates (`tests/templates/`) and deployments (`tests/deployments/`) with a testable Helm chart (`tests/charts/chart1/`) to validate value inheritance. The chart's test files (`tests/charts/chart1/tests/`) assert ATLAS's inheritance behavior, not the chart itself.

### Core Template Pipeline

1. **templates/helmfile.all.yaml.gotmpl** — Discovers clusters by scanning for directories containing `/apps` subdirectories, filters to leaf clusters, then collects deployments at three hierarchy levels (global, group, cluster). Outputs helmfile entries pointing to the single deployment renderer.

2. **templates/helmfile.single.yaml.gotmpl** — Renders a single deployment by loading values from multiple sources in priority order (global → group → cluster → deployment level), decrypting SOPS values, and rendering the deployment's `deployment.yaml` as a Go template.

3. **templates/_functions.tpl** — Reusable helper library (~410 lines) providing: SOPS decryption (`atlas.decryptSopsNested`), list override application, path conversion, and custom glob pattern matching (`*`, `**`, `?`).

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

SOPS is used for per-key encryption in `*.values.sops.yaml` files. The `atlas.decryptSopsNested` function handles recursive decryption of nested encrypted structures via `fetchSecretValue`.

### Atlas Config Object

Templates receive an `atlas` values object containing:
- `cwd` — Absolute working directory path
- `deploymentDefinitions` — Path to deployments directory
- `appTemplates` — Path to application templates
- `debug` — Enable debug output
- `unittest` — Enable unit test mode
- `deployment.cluster` / `deployment.deploymentName` / `deployment.deploymentPath` — Current deployment context (set by the pipeline)
