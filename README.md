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

Think of it as an **atlas of charts** — a structured collection of Helm releases mapped across your cluster topology, with values flowing down through the hierarchy.

ATLAS is **cluster-aware but not cluster-connected** — it knows which deployments belong to which clusters and renders the correct values, but it does not know how to reach the target cluster. Cluster targeting is the responsibility of a deployment automation tool like ArgoCD. ATLAS is designed to work as the rendering backend behind an ArgoCD ApplicationSet (or similar), which handles cluster selection and delivery. It can also be used standalone with `helmfile apply`, but in that case the user must ensure the correct kubeconfig context is active.

### Key Features

- 🌳 **Hierarchical Inheritance** - Global → Group → Cluster value cascade with deep merge
- 🔍 **Automatic Discovery** - No manual cluster registration; directory structure is the config
- 📦 **Pure Helmfile** - Only uses functions included in helmfile, no additional dependencies
- 🎯 **Flexible Structure** - Support for standalone clusters and cluster groups
- 🔐 **SOPS Integration** - Per-key encrypted values at every hierarchy level
- 🚀 **GitOps Native** - Designed for ArgoCD, Fleet, or any declarative workflow

---

## Getting Started

The easiest way to start using ATLAS is to use the [atlas-template](https://github.com/max06/atlas-template) repository as a starting point. It includes:

- A pre-configured `helmfile.yaml.gotmpl` that references ATLAS remotely (no local copy needed)
- An ArgoCD ApplicationSet that automatically discovers and deploys all configured applications
- Example deployments (ArgoCD, Traefik, echo-server) to learn from

```bash
# Clone the starter template
git clone https://github.com/max06/atlas-template my-deployments
cd my-deployments

# List all discovered deployments
helmfile list

# Render a specific deployment
helmfile template --selector cluster=in-cluster,deploymentName=argocd
```

ATLAS is consumed as a remote helmfile reference — your repository only contains your deployments, templates, and values:

```yaml
# helmfile.yaml.gotmpl — your entry point
helmfiles:
  - path: git::https://github.com/max06/atlas.git@helmfile.yaml.gotmpl?ref=v0.1.0
    values:
      - atlas:
          appTemplates: templates
          deploymentDefinitions: deployments
          cwd: {{ exec "pwd" (list) }}
```

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
    namespace: production      # Optional: target namespace
  - template: database         # Multiple apps per deployment
    namespace: production
settings:                      # Settings affecting your argocd application
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

These labels are only applied to helmfile releases by helmfile and serve two purposes:

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

### Template context (for gotmpl value files and app templates)

Every Go template ATLAS renders — app templates (`templates/*/helmfile.yaml.gotmpl`), hierarchy gotmpl files (`global.values.yaml.gotmpl`, `cluster.values.yaml.gotmpl`, …), and values-list gotmpls (`values.yaml.gotmpl` inside a template's `values:` list) — receives a unified context addressable two equivalent ways:

| Syntax | Example |
|--------|---------|
| Wrapped **(recommended)** | `{{ .Values.clusterdomain }}`, `{{ .Values.atlas.cwd }}` |
| Bare | `{{ .clusterdomain }}`, `{{ .atlas.cwd }}` |

Both resolve to the same leaf — ATLAS adds a self-referential `Values` key to every tpl context.

**Use `.Values.xxx` unless you have a reason not to.** Helm charts, helmfile environment values, and every helm-aware linter or IDE completion already speak that dialect; bare `.xxx` is an ATLAS-only shortcut that silently breaks the moment you copy an expression into a helm template (`.Release.Name`, `.Chart.Version`, etc. always require the wrapped form) or paste one from a helm chart example. Consistent `.Values.xxx` lets expressions move between ATLAS values, helm chart values, and standard helmfile environments without rewrites. The bare form stays supported for backward compatibility, but isn't the path of least surprise.

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
| 4 | Instance inline | `apps[].values` list in `deployment.yaml` | Per app instance |
| 5 | Global | `global.values.*` | All clusters, all deployments |
| 6 | Group | `{group}/group.values.*` | All clusters in that group |
| 7 | Cluster | `{cluster}/cluster.values.*` | All deployments on that cluster |
| 8 | Deployment | `{deployment}/values.*` | Only that specific deployment |

Template-include and template-defaults are entries in the Helmfile release's `values:` list inside an app template. The list is ordered — the **last element has highest priority**. The table above shows the conventional ordering.

### File Types (for hierarchy levels 5–8, loaded in this order per level)

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
 4. apps[].values in deployment.yaml                                   ← instance inline
 5. deployments/global.values.sops.yaml                                ← ATLAS hierarchy begins
 6. deployments/global.values.yaml
 7. deployments/global.values.yaml.gotmpl
 8. deployments/{group}/group.values.sops.yaml
 9. deployments/{group}/group.values.yaml
10. deployments/{group}/group.values.yaml.gotmpl
11. deployments/{group}/{cluster}/cluster.values.sops.yaml
12. deployments/{group}/{cluster}/cluster.values.yaml
13. deployments/{group}/{cluster}/cluster.values.yaml.gotmpl
14. deployments/{group}/{cluster}/apps/{name}/values.sops.yaml
15. deployments/{group}/{cluster}/apps/{name}/values.yaml
16. deployments/{group}/{cluster}/apps/{name}/values.yaml.gotmpl       ← highest priority
```

For a **standalone cluster** (no group), steps 8–10 are skipped entirely.

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

2. **Load Values** (`helmfile.single.yaml.gotmpl`) — For each cluster-deployment pair, twin-loads the hierarchy from all levels (global → group → cluster → deployment). The **real** tree loads SOPS values as-is; the **redacted** tree substitutes SOPS leaves with structure-preserving placeholders before merging. Both trees are built in a single template pass. Gotmpl files render once per tree, so any expression referencing a SOPS-derived key automatically produces a redacted derivative in the redacted tree — no explicit taint tracking needed.

3. **Render** (`helmfile.single.yaml.gotmpl`) — Reads the deployment's `deployment.yaml` to find which app templates to instantiate. For each app, renders the template with the real values, progressively merges the release's `values:` list (files decrypted / rendered / inline-mapped in declaration order so later entries can reference earlier keys), overlays the hierarchy for precedence, and collapses the list to a single merged dict. When redaction is enabled, a deep-compare between the real and redacted trees produces a replacement map that the post-renderer uses to substitute secrets in the rendered output.

### Single-deployment rendering (fast path)

When a consumer (e.g. an ArgoCD ApplicationSet) only needs one deployment rendered, ATLAS can short-circuit step 1 so only the matching sub-helmfile is ever loaded. Pass the target via environment variables:

```bash
ATLAS_FILTER_CLUSTER=staging/cluster-a \
ATLAS_FILTER_DEPLOYMENT_NAME=my-app \
  helmfile template
```

Both variables are optional — set either, both, or neither. With neither, the whole repo is rendered as before.

Why env vars? They reach ATLAS regardless of how the consumer's entry-point helmfile is written. A consumer that explicitly lists `appTemplates`, `deploymentDefinitions`, and `cwd` (like the starter template) doesn't forward arbitrary `.Values.atlas` keys, so state-values-based filters would silently drop. Env vars are read inside ATLAS's own top-level file, so the propagation problem goes away.

Why not `--selector`? Helmfile's `--selector` only filters releases **after** every sub-helmfile has been parsed and its values (including SOPS) materialized. That scales poorly for per-deployment invocations. The env-var-driven filter reaches ATLAS's top-level template and skips non-matching entries before helmfile processes them, so SOPS decryption and value rendering only happen for the target deployment.

---

## Secret Redaction

When `redactSecrets` is enabled, ATLAS produces structure-preserving redacted output. Instead of replacing secrets with a flat `REDACTED` marker, the redacted form preserves the shape of the original value so that downstream tooling (ArgoCD diffs, PR review comments) can show meaningful structure.

### Redaction Rules

| Type | Rule | Example |
|------|------|---------|
| String | Split on non-alphanumeric chars; each segment → first min(len, 8) chars of `REDACTED`; delimiters preserved | `mycompany.com` → `REDACTED.RED`, `db` → `RE` |
| Number < 5 digits | Kept (ports, counts) | `5432` → `5432` |
| Number >= 5 digits | Each digit replaced from cycle 1,2,...,9,0; sign and decimal point preserved | `999999` → `123456` |
| Boolean | Kept (50/50 odds) | `true` → `true` |
| Multi-line string | Same segment rules applied per-line; newlines and delimiters preserved | PEM blocks keep their `-----` structure |

### How It Works

ATLAS loads the value hierarchy **twice** in a single template pass:
- The **real** tree loads SOPS values as-is (fed to helm so chart validations pass)
- The **redacted** tree substitutes SOPS leaves with redacted placeholders before merging

A deep-compare between the two trees identifies every leaf that differs, producing a `{real: redacted}` replacement map. This map is base64-encoded and passed to the `atlas-redact` helm post-renderer plugin, which walks every scalar in the rendered YAML and swaps matches structurally using `yq`.

Transitive redaction (a gotmpl value that references a SOPS key) works automatically — re-rendering the gotmpl file with the redacted tree as context produces the redacted derivative without any explicit tracking.

Enable redaction via the `ATLAS_REDACT_SECRETS` environment variable:

```bash
ATLAS_REDACT_SECRETS=true helmfile template
```

---

## CI / Review Workflow

ATLAS provides a reusable GitHub Actions workflow that compares rendered Kubernetes manifests between the target branch and the merge result. It renders both sides in a single job and posts a diff as a PR comment.

### How it works

1. Checks out the **target branch HEAD**, renders all manifests as a baseline
2. Checks out the **merge result** (what will be deployed after merging), renders manifests
3. Generates per-release diffs, posts a sticky PR comment

This "merge-result" strategy ensures the diff answers *"what changes if I hit merge right now?"* — it accounts for changes that landed on main since the PR was created.

### Error handling

- **Target branch render errors** — reported as warnings but do not block merging (the PR may be the fix)
- **Merge-result render errors** — reported and fail the pipeline, with a local-reproduction command in the PR comment
- **Merge ref unavailable** — falls back to PR branch with a prominent warning (likely merge conflicts)
- A **job summary** on the pipeline run page shows which deployments were discovered on each side

### Setup

```yaml
# .github/workflows/atlas-review.yml
name: ATLAS Review
on:
  pull_request:
    branches: [main]

jobs:
  review:
    uses: max06/atlas/.github/workflows/snapshot-review.yml@v0.1.0
    with:
      helmfile-path: helmfile.yaml.gotmpl
    secrets:
      sops-age-key: ${{ secrets.SOPS_AGE_KEY }}
```

### Inputs

| Input | Default | Description |
|-------|---------|-------------|
| `helmfile-path` | `helmfile.yaml.gotmpl` | Path to the helmfile entry point |
| `helmfile-version` | `v1.4.3` | Helmfile version to install |
| `helm-version` | `v4.1.3` | Helm version to install |

### Secrets

| Secret | Required | Description |
|--------|----------|-------------|
| `sops-age-key` | No | SOPS Age private key for decrypting encrypted value files |

If your repository uses SOPS-encrypted value files, the `sops-age-key` secret is **mandatory**. **Recommended: create a dedicated CI age key** rather than reusing a personal or production key:

```bash
age-keygen -o ci-key.txt
```

Add the public key as an additional recipient to your `.sops.yaml` and re-encrypt:

```bash
sops updatekeys deployments/global.values.sops.yaml
```

Store the private key as a GitHub repository secret named `SOPS_AGE_KEY`.
