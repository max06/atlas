# ATLAS

**Application Topology Layer Across Systems**

Navigate your GitOps deployments across any cluster topology with hierarchical inheritance.

**Disclaimer:**
- **Not** created by AI!
- Claude was used to create tests, to find issues and to add a huge amount of code comments.
- Every bit created by AI is controlled and verified by a human.

---

## Overview

ATLAS is a pre-configured Helmfile that automatically discovers clusters in your GitOps repository and assigns applications to them using a hierarchical inheritance model. Define applications once at the global level, override at the group level, or specify at the cluster level — ATLAS handles the rest.

ATLAS is **cluster-aware but not cluster-connected** — it knows which deployments belong to which clusters and renders the correct values, but it does not know how to reach the target cluster. Cluster targeting is the responsibility of a deployment automation tool like ArgoCD. ATLAS is designed to work as the rendering backend behind an ArgoCD ApplicationSet (or similar), which handles cluster selection and delivery. It can also be used standalone with `helmfile apply`, but in that case the user must ensure the correct kubeconfig context is active.

### Key Features

- 🌳 **Hierarchical Inheritance** - Global → Group → Cluster value cascade with deep merge
- 🔍 **Automatic Discovery** - No manual cluster registration; directory structure is the config
- 📦 **Pure Helmfile** - Only uses functions included in helmfile, no additional dependencies
- 🎯 **Flexible Structure** - Support for standalone clusters and cluster groups
- 🔐 **SOPS Integration** - Per-key encrypted values at every hierarchy level
- 🚀 **GitOps Native** - Designed for ArgoCD, Fleet, or any declarative workflow

---

## Directory Structure

```
your-repo/
  helmfile.yaml.gotmpl              # Entry point — includes ATLAS

  deployments/                       # Your deployment definitions
    global.values.yaml               # Values applied to ALL clusters
    global.values.sops.yaml          # Encrypted global values
    global.values.yaml.gotmpl        # Templated global values

    apps/                            # Global deployments (all clusters)
      monitoring/
        deployment.yaml

    staging/                         # A cluster group
      group.values.yaml              # Values for all clusters in "staging"
      apps/                          # Group deployments (all "staging" clusters)
        log-collector/
          deployment.yaml
      cluster-a/                     # A specific cluster in the group
        cluster.values.yaml          # Values for cluster-a only
        apps/                        # Cluster-specific deployments
          my-app/
            deployment.yaml
            values.yaml              # Deployment-specific values
      cluster-b/
        apps/
          my-app/
            deployment.yaml

    standalone-cluster/              # Standalone cluster (no group)
      cluster.values.yaml
      apps/
        my-app/
          deployment.yaml

  templates/                         # App templates (reusable helmfile definitions)
    my-app/
      helmfile.yaml.gotmpl
      values.yaml.gotmpl             # Optional: included values file
    monitoring/
      helmfile.yaml.gotmpl
```

### What goes where

- **Clusters** are directories that contain an `apps/` subdirectory. ATLAS discovers them automatically.
- **Groups** are parent directories of clusters. A cluster at `staging/cluster-a/` belongs to group `staging`.
- **Standalone clusters** sit directly under `deployments/` with no parent group.
- **Deployments** live inside `{cluster}/apps/{name}/` and reference an app template.
- **App templates** are reusable helmfile definitions under `templates/`.

---

## Deployment Definition

Each deployment is a `deployment.yaml` that declares which app templates to instantiate:

```yaml
apps:
  - template: my-app           # Required: references templates/my-app/
    namespace: production      # Required: target namespace
  - template: database         # Multiple apps per deployment
    namespace: production
settings:
  branch: main
  autoSync: false
```

### Using the same template multiple times

When a deployment needs multiple instances of the same template, add a `name` property to disambiguate:

```yaml
apps:
  - template: virtual-machine
    name: vm-primary
    namespace: default
  - template: virtual-machine
    name: vm-secondary
    namespace: default
```

The template author is responsible for using `{{ .Values.atlas.instance.name }}` in the release name to avoid duplicate release IDs.

---

## Labels

ATLAS automatically adds `commonLabels` to every rendered release:

| Label | Value | Purpose |
|-------|-------|---------|
| `cluster` | Cluster path (e.g., `staging/cluster-a`) | Identifies the target cluster |
| `deploymentName` | Deployment directory name (e.g., `my-app`) | Identifies the deployment |

These labels are applied at the Kubernetes resource level by helmfile and serve two purposes:

1. **Deployment automation** — Tools like ArgoCD use these labels to select which deployment to render. For example, an ApplicationSet can pass `--selector cluster=staging/cluster-a,deploymentName=my-app` to helmfile to render a specific cluster-deployment pair.

2. **Custom workflow labels** — The `settings` block in `deployment.yaml` can define additional metadata consumed by your deployment automation. For example, `bootstrap: true` can mark deployments needed for initial cluster setup (like ArgoCD itself and the ATLAS ApplicationSet), allowing a bootstrapping script to filter and apply them before the full GitOps loop is operational.

---

## App Templates

An app template is a `helmfile.yaml.gotmpl` that defines Helm releases. ATLAS renders it with the full merged values as context.

```yaml
# templates/my-app/helmfile.yaml.gotmpl
releases:
  - name: my-app
    chart: my-chart-repo/my-chart
    version: 1.2.3
    namespace: {{ .Values.atlas.instance.namespace | default "default" }}
    values:
      - values.yaml.gotmpl             # Template-include (file path, resolved automatically)
      - replicaCount: 2                # Template-defaults (inline map)
        image:
          tag: latest
```

### Available context

Every app template receives the full merged values as `.Values`, including:

| Key | Description |
|-----|-------------|
| `.Values.atlas.cwd` | Absolute path to the repository root |
| `.Values.atlas.deployment.cluster` | Cluster path (e.g., `staging/cluster-a` or `standalone`) |
| `.Values.atlas.deployment.deploymentName` | Deployment directory name |
| `.Values.atlas.deployment.deploymentPath` | Absolute path to `deployment.yaml` |
| `.Values.atlas.instance.template` | Template name (directory name) |
| `.Values.atlas.instance.name` | Instance name (from `deployment.yaml` `name`, or defaults to template name) |
| `.Values.atlas.appTemplates` | Path to templates directory |
| `.Values.atlas.deploymentDefinitions` | Path to deployments directory |

All hierarchy values (global, group, cluster, deployment) are also available as top-level keys in `.Values`.

---

## Secrets

ATLAS supports SOPS-encrypted values at every hierarchy level using `*.values.sops.yaml` files. Values are decrypted transparently at load time via helmfile's `fetchSecretValue`.

Encrypted files follow the same naming convention as plain files:
- `global.values.sops.yaml`
- `{group}/group.values.sops.yaml`
- `{cluster}/cluster.values.sops.yaml`
- `{deployment}/values.sops.yaml`

All value types are preserved through decryption (strings, numbers, booleans, lists, maps, nested structures).

---

## Value Inheritance Logic

ATLAS loads and merges values from multiple levels. Later sources override earlier ones using deep merge (`mergeOverwrite`).

### Value Levels (lowest → highest priority)

| Priority | Level | Source | Applies to |
|----------|-------------------|------------------------------------------------|--------------------------------------|
| 1 | Chart defaults | Chart's `values.yaml` | Always present |
| 2 | Template-include | File references in template `values:` list | Per app template |
| 3 | Template-defaults | Inline maps in template `values:` list | Per app template |
| 4 | Global | `global.values.*` | All clusters, all deployments |
| 5 | Group | `{group}/group.values.*` | All clusters in that group |
| 6 | Cluster | `{cluster}/cluster.values.*` | All deployments on that cluster |
| 7 | Deployment | `{deployment}/values.*` | Only that specific deployment |

Template-include and template-defaults are entries in the Helmfile release's `values:` list inside an app template. The list is ordered — the **last element has highest priority**. The table above shows the conventional ordering.

### File Types (for hierarchy levels 4–7, loaded in this order per level)

| Order | Suffix | Description |
|-------|------------------|----------------------------------------------|
| 1 | `.sops.yaml` | SOPS-encrypted values (decrypted at load) |
| 2 | `.yaml` | Plain YAML values |
| 3 | `.yaml.gotmpl` | Go-templated values (can reference previously loaded values) |

Within a single level, `.yaml` overrides `.sops.yaml`, and `.yaml.gotmpl` overrides `.yaml`.

### Full Load Order

For a deployment at `deployments/{group}/{cluster}/apps/{name}/deployment.yaml` using app template `{template}`:

```
 1. charts/{chart}/values.yaml                                         ← chart defaults
 2. templates/{template}/values.yaml.gotmpl (or other included files)  ← template-include
 3. inline maps in templates/{template}/helmfile.yaml.gotmpl           ← template-defaults
 4. deployments/global.values.sops.yaml                                ← ATLAS hierarchy begins
 5. deployments/global.values.yaml
 6. deployments/global.values.yaml.gotmpl
 7. deployments/{group}/group.values.sops.yaml
 8. deployments/{group}/group.values.yaml
 9. deployments/{group}/group.values.yaml.gotmpl
10. deployments/{group}/{cluster}/cluster.values.sops.yaml
11. deployments/{group}/{cluster}/cluster.values.yaml
12. deployments/{group}/{cluster}/cluster.values.yaml.gotmpl
13. deployments/{group}/{cluster}/apps/{name}/values.sops.yaml
14. deployments/{group}/{cluster}/apps/{name}/values.yaml
15. deployments/{group}/{cluster}/apps/{name}/values.yaml.gotmpl       ← highest priority
```

For a **standalone cluster** (no group), steps 7–9 are skipped entirely.

### Rules

1. **Deep merge**: Values are merged recursively using `mergeOverwrite`. Map keys from higher-priority sources override lower-priority ones, but sibling keys are preserved.
2. **Missing files are silently skipped**: Any file that does not exist is simply not loaded. No level is mandatory.
3. **Standalone clusters skip group level**: A cluster path without `/` (e.g., `standalone`) has no group; group-level files are not loaded.
4. **Templated values have access to prior values**: `.yaml.gotmpl` files are rendered with all previously loaded values as template context, enabling computed values that reference earlier layers.
5. **Atlas context is always present**: The `atlas` key in the merged values always contains deployment metadata and is not overridden by value files.
6. **Template values list is ordered**: Within an app template's release `values:` list, items are processed in order — last entry wins.
7. **ATLAS hierarchy overrides template values**: The hierarchy values (global → group → cluster → deployment) are applied after the template's own values, ensuring deployment-specific configuration always wins over app template defaults.

---

## How It Works

ATLAS processes your repository in three steps:

1. **Discover** (`helmfile.all.yaml.gotmpl`) — Scans the `deployments/` directory for all directories containing an `apps/` subdirectory. Filters to leaf clusters only (a group directory with child clusters is not itself a target). Collects deployments at three levels: global (`deployments/apps/`), group (`{group}/apps/`), and cluster-specific (`{cluster}/apps/`).

2. **Load Values** (`helmfile.single.yaml.gotmpl`) — For each cluster-deployment pair, loads and merges values from all hierarchy levels (global → group → cluster → deployment), including SOPS decryption. The result is a single merged values dict.

3. **Render** (`helmfile.single.yaml.gotmpl`) — Reads the deployment's `deployment.yaml` to find which app templates to instantiate. For each app, renders the template with the merged values, resolves file paths, and appends the hierarchy values as the highest-priority entry. Outputs complete helmfile release blocks.

---

## CI / Snapshot Review

ATLAS provides a reusable GitHub Actions workflow that compares rendered Kubernetes manifests between the main branch and a pull request. It posts a diff as a PR comment, letting reviewers see the exact impact of deployment changes before merging.

### How it works

1. **On push to main** — Renders all manifests via `helmfile template`, uploads the output as a baseline artifact.
2. **On pull request** — Renders manifests from the PR branch, downloads the latest baseline from main, diffs the two, and posts a sticky comment on the PR.

### Setup

Create a workflow file in your repository:

```yaml
# .github/workflows/atlas-review.yml
name: ATLAS Snapshot Review
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  review:
    uses: max06/atlas/.github/workflows/snapshot-review.yml@main
    with:
      helmfile-path: helmfile.yaml.gotmpl
    secrets:
      sops-age-key: ${{ secrets.SOPS_AGE_KEY }}
```

After creating the workflow, push it to main to generate the first baseline snapshot. Subsequent pull requests will show a diff comment.

### Inputs

| Input | Default | Description |
|-------|---------|-------------|
| `helmfile-path` | `helmfile.yaml.gotmpl` | Path to the helmfile entry point |
| `helmfile-version` | latest | Helmfile version to install |
| `helm-version` | latest | Helm version to install |

### Secrets

| Secret | Required | Description |
|--------|----------|-------------|
| `sops-age-key` | No | SOPS Age private key for decrypting encrypted value files |

### SOPS Secret Redaction

If your repository uses SOPS-encrypted value files (`*.values.sops.yaml`), the `sops-age-key` secret is **mandatory** — without it, `helmfile template` will fail when it encounters encrypted files. **All decrypted values are automatically redacted** — replaced with `*REDACTED*` in the rendered output — so that secrets are never exposed in PR comments or artifacts. The key structure is preserved, so diffs still show which secret keys were added, removed, or moved, without revealing actual values.

**Recommended: create a dedicated CI age key** rather than reusing a personal or production key:

```bash
# Generate a new age key pair for CI
age-keygen -o ci-key.txt
# Output: Public key: age1...
```

Add the public key as an additional recipient to your `.sops.yaml` creation rules:

```yaml
creation_rules:
  - path_regex: values\.sops\.yaml$
    age: >-
      age1your-existing-key...,
      age1your-ci-key...
```

Re-encrypt all existing SOPS files so they include the new recipient:

```bash
# For each encrypted file:
sops updatekeys deployments/global.values.sops.yaml
```

Then store the **private key** (the contents of `ci-key.txt`) as a GitHub repository secret named `SOPS_AGE_KEY`.

This redaction is controlled by the `ATLAS_REDACT_SECRETS` environment variable, which the workflow sets automatically. You can also use it locally:

```bash
ATLAS_REDACT_SECRETS=true helmfile -f helmfile.yaml.gotmpl template
```

### What to expect

- **First run**: The PR comment will say "No baseline snapshot found." Push to main first to create a baseline.
- **No changes**: The comment will confirm no changes were detected.
- **Changes detected**: The comment shows a collapsible diff with the full rendered output comparison.
- **Large diffs**: If the diff exceeds GitHub's comment size limit, it is truncated with a note to download the full snapshot artifact.
