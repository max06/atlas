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
settings:                      # Settings consumed by ArgoCD and ATLAS
  namespace: production        # Optional: deployment-wide namespace default
  branch: main
  autoSync: false
```

### Using the same template multiple times

When a deployment needs multiple instances of the same template, add a `name` property to disambiguate:

```yaml
apps:
  - template: virtual-machine
    name: primary
    namespace: default
  - template: virtual-machine
    name: secondary
    namespace: default
  - template: virtual-machine
    name: cust-abc
    namespace: default
    nameStyle: suffix       # optional, default: prefix
```

ATLAS automatically rewrites release names so each instance produces a distinct release — the template author writes the bare release name (e.g. `vm`) and never touches `{{ .Values.atlas.instance.name }}`. With the example above:

| `apps[].name` | `nameStyle` | Resulting release name |
|---------------|-------------|------------------------|
| _(unset)_                | _(n/a)_      | `vm` (no munge — instance name defaults to template name) |
| `primary`                | _(default)_  | `primary-vm`           |
| `secondary`              | `prefix`     | `secondary-vm`         |
| `cust-abc`               | `suffix`     | `vm-cust-abc`          |

`prefix` is the default. `suffix` is offered for naming conventions (e.g. multi-tenant `vm-cust-abc`) where the leading label reads better at the end. Templates remain readable as vanilla helmfile when no `name` is set.

---

## Namespace Handling

ATLAS does not require a namespace on releases — templates, deployments, and even the final rendered manifests can omit it entirely. When no namespace is set at any level, ArgoCD applies `destination.namespace` to resources that lack `metadata.namespace` in the manifest. Cluster-scoped resources (CRDs, ClusterRoles, etc.) are never affected by namespace settings.

### Namespace Precedence (highest wins)

| Priority | Source | Description |
|----------|--------|-------------|
| 1 | Template release `namespace:` | Defined by the template author in `helmfile.yaml.gotmpl` |
| 2 | `settings.namespace` | Deployment-wide default in `deployment.yaml` — applies to all releases |
| 3 | `apps[].namespace` | Per-instance override in `deployment.yaml` — most specific |

Higher-priority sources override lower ones. If none are set, the release has no namespace.

### Examples

```yaml
# Template defines namespace: monitoring
# deployment.yaml — no override → release uses "monitoring"
apps:
  - template: kube-prometheus

# deployment.yaml — settings.namespace overrides all template namespaces
settings:
  namespace: observability
apps:
  - template: kube-prometheus     # → namespace: observability

# deployment.yaml — apps[].namespace overrides settings.namespace
settings:
  namespace: default
apps:
  - template: virtual-machine
    name: vm-a                    # → namespace: default (from settings)
  - template: virtual-machine
    name: vm-b
    namespace: tenant-b           # → namespace: tenant-b (instance wins)
```

### ArgoCD integration

The ATLAS ApplicationSet template sets `destination.namespace` to `settings.namespace` when defined, falling back to the deployment name (`appInstance`) otherwise. This namespace is cosmetic for resources that already carry a namespace in the rendered manifest — ArgoCD only applies `destination.namespace` to resources that lack `metadata.namespace`.

The helmfile-argocd-plugin passes `--namespace $ARGOCD_APP_NAMESPACE` by default, which overrides **all** release namespaces at the helmfile CLI level — ATLAS cannot prevent this. To preserve template and deployment-level namespace settings, set `HELMFILE_USE_CONTEXT_NAMESPACE=true` in the ApplicationSet plugin configuration. This tells the plugin to skip the `--namespace` flag, letting ATLAS's namespace precedence take effect.

---

## Labels

ATLAS automatically adds `commonLabels` to every rendered release:

| Label | Value | Purpose |
|-------|-------|---------|
| `cluster` | Full cluster path (e.g., `staging/cluster-a`) | Target cluster |
| `clusterName` | Leaf cluster name (e.g., `cluster-a`) | Target cluster (path-safe form) |
| `clusterGroup` | Group prefix (e.g., `staging`) — only for grouped clusters | Group filtering |
| `deploymentName` | Deployment directory name | Deployment identity |
| `template` | App template directory name | Filter by template family (ApplicationSet selectors) |
| `instance` | `apps[].name`, or template name when unset | Disambiguate multi-instance deployments |
| `variant` | Variant label (defaults to `default`) | Multiple ATLAS variants on one cluster |

These labels are only applied to helmfile releases by helmfile and serve two purposes:

1. **Deployment automation** — Tools like ArgoCD use these labels to select which deployment to render. For example, an ApplicationSet can pass `--selector cluster=staging/cluster-a,deploymentName=my-app` to helmfile to render a specific cluster-deployment pair.

2. **Custom workflow labels** — The `settings` block in `deployment.yaml` can define additional metadata consumed by your deployment automation. For example, `bootstrap: true` can mark deployments needed for initial cluster setup (like ArgoCD itself and the ATLAS ApplicationSet), allowing a bootstrapping script to filter and apply them before the full GitOps loop is operational.

---

## App Templates

An app template is a `helmfile.yaml.gotmpl` that defines Helm releases. ATLAS renders it with the full merged values as context. Templates aim to stay close to vanilla helmfile so they can be tested without ATLAS and migrated away if needed — paths and release-name disambiguation are handled by the renderer.

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

### How ATLAS treats helmfile release options

The per-instance renderer rewrites each release before it reaches helmfile. Most fields pass through; a handful get specific treatment so templates remain readable and free of ATLAS-specific boilerplate:

| Field | ATLAS behavior |
|-------|----------------|
| `name` | Auto-munged with `instance.name` when the deployment sets `apps[].name` and it differs from the template name. `apps[].nameStyle` (`prefix` default, `suffix` opt-in) controls form. Templates should not embed `{{ .Values.atlas.instance.name }}`. |
| `chart` | Relative paths (`./` / `../`) resolved template-relative. |
| `values` | Replaced wholesale with the values-loader; the loader walks the hierarchy + template + instance lists with progressive merge. |
| `secrets` | Stripped from emitted state and merged AFTER all values lists by the loader (helmfile env-secret semantics). Paths resolve template-relative for `release.secrets`, deployment-relative for `apps[].secrets`. |
| `condition`, `installed` | Stripped — deployment intent is the presence of `deployment.yaml`. |
| `skipSchemaValidation` | Defaulted to `true` when absent (preserves user-set true OR false). |
| `postRenderer` | Rejected with a fail message — ATLAS owns this slot for redaction; helm 4 cannot chain post-renderers. |
| `keyring`, `set[].file`, `setString[].file` | Relative paths (`./` / `../`) resolved template-relative. |
| `strategicMergePatches`, `jsonPatches`, `transformers` | Path-resolved template-relative; instance-level overrides from `deployment.yaml` are appended. |
| `labels` | Instance-level `apps[].labels` from `deployment.yaml` merged with instance taking priority. ATLAS's commonLabels (cluster, template, instance, …) are added on top. |

### Available context

Every app template receives the full merged values as `.Values`, including:

| Key | Description |
|-----|-------------|
| `.Values.atlas.cwd` | Absolute path to the repository root |
| `.Values.atlas.deployment.cluster` | Full cluster path (e.g., `staging/cluster-a` or `standalone`), used for filesystem layout |
| `.Values.atlas.deployment.clusterName` | Leaf cluster name only (e.g., `cluster-a`). Use for naming downstream resources (ArgoCD apps, etc.) where the group-prefixed path would contain an illegal `/`. Leaf names must be unique across the repo. |
| `.Values.atlas.deployment.clusterGroup` | Group prefix of the cluster path (e.g., `staging` for `staging/cluster-a`). Absent for standalone clusters — guard with `{{ if .Values.atlas.deployment.clusterGroup }}` to activate group-scoped features. |
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

### Rendering without secrets

Set `ATLAS_SKIP_SECRETS=true` to render without decrypting any SOPS file:

```bash
ATLAS_SKIP_SECRETS=true helmfile template
```

Every `*.sops.yaml` file (hierarchy files, `.sops.yaml` entries in `values:` lists, and `secrets:` files) contributes its keys with the encrypted `ENC[...]` strings as values. No SOPS key, gpg agent or age key is needed, and the render makes no decryption calls. Values that templates derive from a secret are derived from the `ENC[...]` string instead.

Use it for renders that do not need the real secrets: structure checks, diffs of non-secret changes, automation that must not see secrets, or machines without key access. The output is **not deployable** — the rendered secrets are ciphertext. Charts that validate secret values (length, format) can fail on the `ENC[...]` strings.

`ATLAS_SKIP_SECRETS` and `ATLAS_REDACT_SECRETS` can be combined: the post-renderer still structurally redacts every `v1/Secret`, and there is nothing to redact from the values.

---

## Value Inheritance Logic

ATLAS loads and merges values from multiple levels. Later sources override earlier ones using deep merge (`mergeOverwrite`).

### Value Levels (lowest → highest priority)

| Priority | Level | Source | Applies to |
|----------|-------------------|------------------------------------------------|--------------------------------------|
| 1  | Chart defaults    | Chart's `values.yaml`                            | Always present |
| 2  | Template-include  | File references in template release `values:` list | Per app template |
| 3  | Template-defaults | Inline maps in template release `values:` list   | Per app template |
| 4  | Template secrets  | `release.secrets` list (SOPS files)              | Per app template |
| 5  | Global            | `global.values.*`                                | All clusters, all deployments |
| 6  | Group             | `{group}/group.values.*`                         | All clusters in that group |
| 7  | Cluster           | `{cluster}/cluster.values.*`                     | All deployments on that cluster |
| 8  | Deployment        | `{deployment}/values.*`                          | Only that specific deployment |
| 9  | Instance inline   | `apps[].values` list in `deployment.yaml`        | Per app instance |
| 10 | Instance secrets  | `apps[].secrets` list (SOPS files)               | Per app instance |

Template values are defaults: every file of the directory hierarchy overrides them. The instance (`apps[]` in `deployment.yaml`) is the most specific scope — one app instance of one deployment — so it overrides the hierarchy, including the deployment's own `values.*` files (one deployment can hold several instances).

Template-include and template-defaults are entries in the Helmfile release's `values:` list inside an app template. The list is ordered — the **last element has highest priority**. The table above shows the conventional ordering.

Secrets are merged AFTER the `values:` entries of their layer, regardless of declaration order: `release.secrets` after the template's `values:`, `apps[].secrets` after the instance's `values:`. This mirrors helmfile's env-level "non-HCL secrets are loaded first but merged last" contract — secret leaves win over plain-value leaves with the same key on the same layer. Hierarchy files still override template secrets, and instance values still override hierarchy secrets (`*.values.sops.yaml`): the layer order wins over the secret rule.

Before v0.10.0 the hierarchy overrode the instance (`apps[].values` sat between template-defaults and template secrets). A value set inline on an app instance was silently replaced by any hierarchy file that set the same key. Consumers that relied on that must move those values into the hierarchy file of the matching scope.

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
 4. release.secrets in templates/{template}/helmfile.yaml.gotmpl       ← template secrets
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
16. deployments/{group}/{cluster}/apps/{name}/values.yaml.gotmpl       ← hierarchy ends
17. apps[].values in deployment.yaml                                   ← instance inline
18. apps[].secrets in deployment.yaml                                  ← instance secrets, highest priority
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

## atlas CLI (proof of concept)

`cmd/atlas` is a command-line driver for ATLAS repositories. It sets the `ATLAS_*` switches from flags, applies the production render flags, and compares revisions for reviews. It runs the `helmfile` binary; the ATLAS templates stay versioned by the repository's entry helmfile, so the CLI and the template version are independent.

```bash
go install github.com/max06/atlas/cmd/atlas@<ref>

atlas render -c cluster -d my-app            # manifests to stdout, secrets stay ENC[...], no key needed
atlas render -c group/cluster -d my-app --decrypt   # with decrypted SOPS values (needs the key)
atlas inspect -c cluster -d my-app           # the helmfile states ATLAS generates
atlas discover                               # the discovery map as JSON
atlas review classify                        # which pairs your working tree affects
atlas review diff --patch                    # render them on both sides and diff
atlas review diff --format markdown          # the PR comment the review workflow would post
atlas review diff -c cluster -d my-app --base origin/main   # exactly this deployment on both sides, changed or not
atlas doctor                                 # helm / helmfile / git versions
atlas discover --engine native               # the discovery map from the native Go engine (experimental)
```

**Container image and wrapper.** `ghcr.io/max06/atlas:<version>` carries the CLI plus the toolchain it is tested with (helm, helmfile, kustomize, sops, the redaction plugin). Copy [`contrib/atlas-wrapper.sh`](contrib/atlas-wrapper.sh) into a consumer repository as `bin/atlas`; it needs only docker and git. The wrapper mounts the repository at its own absolute path (and each output path outside it: `-o`/`--out`/`--output-dir`, `--result`), runs as your uid, keeps charts and helmfile states in the named volume `atlas-cache`, and passes `SOPS_AGE_KEY_FILE` only with `--decrypt`/`--redact`. `ATLAS_IMAGE=<ref>` runs another build (for example a branch test image); otherwise the tag comes from `spec.engine` in [`atlas.yaml`](#atlasyaml-the-engine-pin), then from the wrapper's default. A moving atlas ref in the entry helmfile (a branch) stays cached in the volume: `docker volume rm atlas-cache` refreshes it.

**Native engine (experimental).** `--engine native` runs the logic of the ATLAS templates in Go instead of the helmfile templates that the entry pins: discovery, the deployment definitions, the hierarchy and instance values (SOPS included), the release rewrite and the redaction. The engine writes plain helmfile states, and its renderer renders them without helmfile and without the helm binary (helm SDK, chartify and kustomize in-process; `ATLAS_NATIVE_RENDERER=helmfile` renders them with helmfile instead): no ATLAS template is evaluated. The engine reads the directories from `atlas.yaml` (or `--templates`, `--deployments`). Its tpl context is the one of helmfile (sprig plus helmfile's functions, the atlas templates directory as base path with the helpers of `templates/_*.tpl`), so templates and values files render the same.

```bash
atlas discover --engine native               # the discovery map
atlas render --engine native -c cluster1 -d app  # manifests (keyless by default, --decrypt for SOPS)
atlas render --engine native --redact -o out      # decrypted, then redacted as the review does
```

The oracle is the template engine: `ATLAS_ENGINE=native` (with `ATLAS_CLI`) runs the core bats suites against the native engine through a helmfile shim, and full renders of consumer trees are compared file by file. `--redact` computes the redaction maps with the native engine and scrubs the tree like the review's render step (Secret values hashed, the maps replayed; a release that cannot be redacted fails the command and nothing is written). The review pipeline still uses the template engine.

`-C <dir>` points at the repository, `-f <file>` at the entry helmfile (default `helmfile.yaml.gotmpl`). `-c` takes the full cluster path or the leaf name; an unknown cluster or deployment fails with the nearest known names.

**Keyless by default.** `render`, `inspect --values` and `review diff` decrypt nothing unless you pass `--decrypt` (or `--redact`, which decrypts and then redacts): SOPS values stay `ENC[...]`, no key is needed, no secret reaches the terminal. That needs an ATLAS version that honors `ATLAS_SKIP_SECRETS` — an older one ignores the variable and decrypts. The CLI therefore reads the `capabilities` of the discovery map first (a probe of well under a second) and stops with a hint when the pinned ATLAS cannot render keyless. `--argocd` always decrypts (the production path).

**Flags are the interface.** The `ATLAS_*` variables the CLI controls (`ATLAS_FILTER_*`, `ATLAS_SKIP_SECRETS`, `ATLAS_REDACT_SECRETS`, `ATLAS_DISCOVERY_MAP`, `ATLAS_SIDEDUMP_MAP_DIR`) are ignored in the caller's environment, with a warning. A plain `helmfile` call with those variables keeps working.

**Reviews compare the current target with the merge result.** By default:

- the base is `origin/<default branch>` as it is in the clone (the local `main` when no remote-tracking ref exists). The CLI never fetches or writes refs, exactly like `git diff origin/main`; the printed base carries its commit date, so run `git fetch` yourself when it looks old (a stale base shows newer target commits as reverts). `--base` overrides it, for example `--base origin/canary` for a branch cut from canary;
- the other side is the working tree, including uncommitted and untracked files. It is snapshotted through a temporary index, so your index and `git status` stay untouched. `--head <rev>` reviews a commit instead;
- the two are merged with `git merge-tree`, without a checkout. A conflict stops the review and names the files.

Both sides are exported into one workspace path in turn, because rendered values embed the checkout path. In CI, the platform already made the merge commit, and a shallow clone has no history to merge in: pass both commits with `--no-merge`.

**A target side that does not render stops the review.** A diff against it would show every selected release as added. `--allow-base-error` compares anyway (the change may be the fix).

`atlas review classify` is a port of the review pipeline's `classify.sh`. Its precomputed mode (`--map-base`, `--map-head`, `--changes-file`, `--out`) writes the same output set, and the classify scenarios compare both when `ATLAS_CLI` points at a binary. `atlas review diff` compares rendered files byte for byte.

**The CI review is the CLI.** The review workflow runs `atlas review ci action review` in the atlas image (action `atlas-review`). The steps of the former bash pipeline are subcommands of `atlas review ci` (`render`, `discover`, `scrub`, `replay`, `diff`, `shadow`, `comment`) with the same environment contract as the scripts; the scripts under `.github/actions/*/` remain as the reference implementation of the workflow tests.

`atlas render --argocd` runs as the Argo CD config management plugin: the filter comes from `ARGOCD_ENV_ATLAS_FILTER_CLUSTER` / `ARGOCD_ENV_ATLAS_FILTER_DEPLOYMENT_NAME`, and releases render into `ARGOCD_APP_NAMESPACE` unless `ARGOCD_ENV_HELMFILE_USE_CONTEXT_NAMESPACE` is set.

## atlas.yaml: the engine pin

`atlas.yaml` at the root of the tree (the Argo CD source path) pins the engine that renders the tree. Each branch has its own copy. To test another engine, change the pin in a PR or in your working tree.

```yaml
apiVersion: atlas.io/v1alpha1
kind: Root
spec:
  engine: "0.13"              # engine minor: "<major>.<minor>", a quoted string
  templateDir: templates      # optional, default "templates"
  deploymentDir: deployments  # optional, default "deployments"
```

Rules:

- `engine` must be a quoted string. An unquoted `1.10` is the float `1.1` for every YAML reader, and it would select a different engine. atlas rejects it.
- Only `apiVersion`, `kind`, `metadata` and `spec` are permitted at the top level. An unknown key is an error, so a misspelled key cannot fall back to a default silently.
- `templateDir` and `deploymentDir` are relative paths inside the tree. At this time the entry helmfile still sets the directories (`atlas.appTemplates`, `atlas.deploymentDefinitions`). atlas validates the two keys but does not use them yet.

The pin selects:

- the image of the developer wrapper (`bin/atlas`),
- the engine that renders each side of a review ([Reviews across engine versions](#reviews-across-engine-versions)), and
- the Argo CD plugin that renders the Applications of the tree (next section).

A tree without `atlas.yaml` renders as before, through the plugin that the consumer has installed.

### Argo CD: plugin selection by discovery

Each engine minor runs as its own config management plugin sidecar, `atlas-v<major>-<minor>`. The image writes this plugin config itself (`atlas cmp config`). Applications do not name a plugin. The repo-server asks each sidecar in turn, and the discovery of each sidecar runs `atlas cmp match`. This command reads `atlas.yaml` in the source path and matches only in the sidecar of the pinned engine.

Argo CD behaviour that the contract depends on (Argo CD 3.4 and 3.5):

- Without `spec.source.plugin.name`, the repo-server asks the sidecars in alphabetical order of their socket names. The first match wins. If two sidecars match, Argo CD gives no error. Thus the matches must exclude each other.
- A match is exit code 0 and output on stdout. A non-zero exit code is an error of this sidecar only, and the repo-server continues with the next sidecar. When no sidecar matches, the Application shows the error of the last sidecar that it asked. Thus `atlas cmp match` gives the same error in every sidecar.
- Each discovery call sends the source path as an archive to the sidecar. Each sidecar that does not match adds one archive for each manifest generation. Keep the number of sidecars small (normally two during an upgrade).

| Part | Requirement |
|---|---|
| `atlas.yaml` | at the root of the Application source path, valid against the schema above |
| atlas sidecar | the image `ghcr.io/max06/atlas:<release>` with its built-in plugin config; one sidecar for each engine minor in use |
| your own helmfile plugin (trees without `atlas.yaml`) | discovery `atlas cmp match --legacy`. Install it **before** the first atlas sidecar: a discovery that matches every helmfile tree also matches the trees of the atlas sidecars |
| ApplicationSet / Application | no `plugin.name`; plugin `env` as before; add `/atlas.yaml` to `argocd.argoproj.io/manifest-generate-paths` |

`atlas cmp match` gives these results:

| `atlas.yaml` | atlas sidecar of the pinned minor | other atlas sidecars | `--legacy` |
|---|---|---|---|
| does not exist | no match | no match | match |
| pins an installed minor | match | no match | no match |
| pins a minor without a sidecar | error: names the pin and the installed plugins | same error | same error |
| invalid (unquoted engine, unknown key, YAML error) | error with the reason | same error | same error |

A sidecar for one engine minor (repo-server patch):

```yaml
- name: atlas-v0-13
  image: ghcr.io/max06/atlas:0.13.0
  command: [/var/run/argocd/argocd-cmp-server]
  securityContext: {runAsNonRoot: true, runAsUser: 999}
  env:
    - {name: SOPS_AGE_KEY_FILE, value: /sops/age/keys.txt}
  volumeMounts:
    - {mountPath: /var/run/argocd, name: var-files}
    - {mountPath: /home/argocd/cmp-server/plugins, name: plugins}
    - {mountPath: /cache, name: atlas-v0-13-cache}   # emptyDir
    - {mountPath: /sops/age, name: age-secret-keys}
```

Your own helmfile plugin needs the `atlas` binary for its discovery. The binary is static: an init container copies it from the atlas image into a shared volume. Then replace the `discover` block of the plugin config (for example with a ConfigMap mounted over `/home/argocd/cmp-server/config/plugin.yaml`):

```yaml
initContainers:
  - name: atlas-bin
    image: ghcr.io/max06/atlas:0.13.0
    command: [cp, /usr/local/bin/atlas, /atlas-bin/atlas]
    volumeMounts: [{mountPath: /atlas-bin, name: atlas-bin}]   # emptyDir
# plugin config of your helmfile plugin:
spec:
  discover:
    find:
      command: [/atlas-bin/atlas, cmp, match, --legacy]
```

Upgrade order for a new engine minor: install the new sidecar → change the pin on a test branch → change the pin on the other branches → remove the old sidecar when no branch pins it. To roll back, revert the pin.

A build without a release version (branch images) gets the plugin `atlas-dev` without discovery: only Applications that name it with `plugin.name` use it. `atlas cmp config --engine <minor>` writes a config with discovery for such a build (tests).

---

## How It Works

ATLAS processes your repository in four stages, each implemented in its own helmfile/gotmpl:

1. **Discover** (`templates/helmfile.all.yaml.gotmpl`) — Scans the `deployments/` directory for all directories containing an `apps/` subdirectory. Filters to leaf clusters only (a group directory with child clusters is not itself a target). Collects deployments at three levels: global (`deployments/apps/`), group (`{group}/apps/`), and cluster-specific (`{cluster}/apps/`). Emits one sub-helmfile entry per (cluster, deployment) pair.

2. **Fan out per instance** (`templates/helmfile.single.yaml.gotmpl`) — For each (cluster, deployment) pair, reads the deployment's `deployment.yaml` and emits one sub-helmfile per app instance. Multi-instance deployments produce one file per instance because helmfile's environment values collapse multi-`environments:` blocks to a single merged value — the per-instance split keeps `atlas.instance` unambiguous.

3. **Per-instance rewrite** (`templates/helmfile.instance.yaml.gotmpl`) — Reads the chosen app template's `helmfile.yaml.gotmpl`, then rewrites each release in place: chart paths and patch/transformer paths resolved template-relative; `release.values` replaced with a single pointer to the values-loader; `condition` / `installed` / `secrets` stripped (loader handles secrets); `postRenderer` rejected as ATLAS-owned; `skipSchemaValidation` defaulted on; instance-level labels merged in; release name auto-munged with the instance name when needed; commonLabels (cluster, clusterName, clusterGroup, deploymentName, template, instance, variant) injected. When `redactSecrets` is on, the loader is called twice (`redact=true` + `redact=false`); the diff becomes the per-release replacement map for the `atlas-redact` helm post-renderer, which scrubs secrets out of the rendered manifests on stdin.

4. **Resolve values at release time** (`templates/_values_loader.tpl`, `templates/helmfile.values-loader.yaml.gotmpl`) — Helmfile evaluates the loader once per release with `.Release.*` and the full atlas object available. The loader walks the hierarchy (global → group → cluster → deployment, three file types per level: `.sops.yaml`, `.yaml`, `.yaml.gotmpl`), builds a baseline, processes the template's `values:` list and the instance's `values:` list with progressive merge so later entries can reference earlier keys, applies `release.secrets` and `apps[].secrets` AFTER values (matching helmfile's env-secret precedence), then overlays the hierarchy as the final precedence layer. Returns one merged YAML blob — what helm sees.

The split lets each stage do exactly one thing and lets the loader run inside helmfile's release-evaluation phase, where `.Release.*` is finally available.

### Single-deployment rendering (fast path)

Two complementary filters exist:

- **`--selector`** — the standard helmfile flag; matches release-level `commonLabels` (`cluster`, `clusterName`, `clusterGroup`, `deploymentName`, `template`, `instance`). It filters *after* every sub-helmfile is parsed, so it does not avoid SOPS decryption of unrelated deployments.
- **`ATLAS_FILTER_CLUSTER` / `ATLAS_FILTER_DEPLOYMENT_NAME`** — stage-1 environment filters: non-matching clusters and deployments are dropped in the discovery stage, before any sub-helmfile is emitted. This is the cheap path for per-deployment renders (ArgoCD ApplicationSet pattern: one ATLAS render per Application). Both take comma-separated lists; both set means the cross product.

```bash
# one deployment
ATLAS_FILTER_CLUSTER=staging/cluster-a ATLAS_FILTER_DEPLOYMENT_NAME=my-app helmfile template

# several deployments of one cluster in one invocation
ATLAS_FILTER_CLUSTER=staging/cluster-a ATLAS_FILTER_DEPLOYMENT_NAME=my-app,other-app helmfile template
```

The filter matches the cluster *path* (`staging/cluster-a`), not the leaf name.

### Discovery map

`ATLAS_DISCOVERY_MAP=1` makes ATLAS describe what it would render instead of rendering it: `helmfile build` then prints one release-less state whose values hold every `(cluster, deployment)` pair, its `deployment.yaml` path (repo-relative) and the app templates it instantiates.

```bash
ATLAS_DISCOVERY_MAP=1 helmfile build --allow-no-matching-release \
  | yq eval-all -o=json '[.]' - \
  | jq '[.[] | .renderedvalues.atlasDiscovery? // empty] | .[0]'
```

```json
{
  "version": 2,
  "capabilities": ["skipSecrets", "multiValueFilter", "redactSecrets"],
  "deploymentsRoot": "deployments",
  "templatesRoot": "templates",
  "pairs": [
    { "cluster": "staging/cluster-a", "clusterName": "cluster-a", "clusterGroup": "staging",
      "deploymentName": "my-app", "deploymentPath": "deployments/staging/apps/my-app/deployment.yaml",
      "templates": ["my-app"], "charts": ["charts/my-chart"] }
  ]
}
```

`capabilities` lists the runtime switches this ATLAS version honors (`ATLAS_SKIP_SECRETS`, comma lists in `ATLAS_FILTER_*`, `ATLAS_REDACT_SECRETS`). An older ATLAS ignores an unknown `ATLAS_*` variable silently — it would, for example, decrypt SOPS files although `ATLAS_SKIP_SECRETS` is set — so callers read this list before they rely on a switch. A build filtered to a cluster that does not exist (the probe) returns the header with zero pairs in well under a second. New switches append their name; the version stays.

`charts` lists the local chart directories (repo-relative) the pair's releases render from, including `file://` subcharts; ATLAS renders each app template once with the SOPS-free state-build context to read its `chart:` fields. Remote charts are not listed.

One discovery pass, no values loader, no SOPS — a few seconds on a repo with a few hundred deployments. The review workflow uses it to pick the render subset for a PR (see below); it is also the right input for anything else that needs the repo's dependency graph (e.g. ArgoCD `manifest-generate-paths` annotations).

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

### Where the redaction runs

Two transports exist for the per-release replacement maps the templates compute:

- **`ATLAS_REDACT_SECRETS=true`**: the map travels to the `atlas-redact` helm post-renderer as one base64 argument, which redacts the manifests inside `helm template`. A single argument is capped by the kernel at 128 KiB: a release whose secret-derived content is large (a virtual-cluster definition that embeds a registration manifest) fails with `argument list too long`.
- **`ATLAS_REDACT_MAPS=1`**: no post-renderer. Every app instance hands its maps out through a release-less carrier state, so `helmfile build --allow-no-matching-release` prints them as that state's rendered values (`atlasRedactMaps.{cluster, deploymentName, releases{<name>: map}}`), with no size limit. The CI review uses this mode when the templates list the `redactMaps` capability: it collects the maps, renders unredacted, and redacts the rendered tree itself (the same Go scrubber that replays maps over an older baseline: structural Secret markers, then the map). Older templates fall back to the post-renderer.

## CI / Review

ATLAS reviews a pull request by rendering what the merge would deploy and what the target branch deploys today, and posts the manifest diff as a PR comment. The review is a GitHub action: `uses: max06/atlas@<release>` in a job you own.

### How it works

1. The action checks out the **merge result** (GitHub's trial merge: what will be deployed after merging) with fetch depth 2, so its first parent, the **target commit** the merge was made against, is the baseline. Without a merge ref (conflicts) the PR branch is compared with its merge-base. The checkout keeps no credentials.
2. ONE container step (the atlas CLI, image `ghcr.io/max06/atlas`): discovery maps of both commits, classification of the changed paths, the render of the merge result (with its redaction maps), the render of the target commit from the same checkout path, the redaction replay over the target render, per-resource semantic diffs (dyff), the shadow check.
3. A sticky PR comment and a job summary with the complete diff. Outputs for your own gates.

This "merge-result" strategy makes the diff answer *"what changes if I merge right now?"*: it accounts for changes that landed on the target branch since the PR was opened.

### Setup

Copy this workflow into your repository and own it: trigger, secrets, runner, concurrency and the required check are your policy. The action does the rest.

```yaml
# .github/workflows/atlas-review.yml
name: ATLAS Review
on:
  pull_request:
    branches: [main]
    # `edited` reviews again when the base branch of the PR changes
    types: [opened, synchronize, reopened, edited]

permissions: {}

concurrency:
  group: atlas-review-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  review:
    runs-on: ubuntu-latest
    permissions:
      contents: read        # checkout of the merge result
      pull-requests: write  # the sticky comment
    steps:
      - uses: max06/atlas@v0.11.0
        id: atlas
        with:
          helmfile-path: helmfile.yaml.gotmpl
          render-subset: "on"
          sops-age-key: ${{ secrets.SOPS_AGE_KEY }}

  # A fixed job name for the branch ruleset. No path filter on the trigger:
  # a workflow skipped by a path filter leaves a required check pending.
  atlas-review-result:
    needs: review
    if: always()
    runs-on: ubuntu-latest
    steps:
      - run: |
          [ "${{ needs.review.result }}" = "success" ] || { echo "::error::ATLAS review: ${{ needs.review.result }}"; exit 1; }
```

**Pin a release.** `max06/atlas@v0.11.0` pins the action, the CLI and the container image to one version (the image follows the reference the action is used with), so a review is reproducible and a bump is one Renovate or Dependabot PR. `@main` is the development branch.

**Large repositories:** a full render (entry or global changes) can exhaust a standard runner; put the job on a larger one (`runs-on`). Set `render-parallel` and `helmfile-timeout` only when the defaults (CPUs of the runner, 900 s per helmfile call) do not fit.

**Keeping the key away from feature branches:** a repository secret is readable by every workflow on every branch. To limit the key to protected branches, store `SOPS_AGE_KEY` in a GitHub environment that only those branches may use, put `environment: <name>` on the review job, and trigger on `pull_request_target` (GitHub then takes the workflow from the target branch; skip PRs from forks, whose templates would run with the key present: `if: github.event.pull_request.head.repo.full_name == github.repository`). The render still runs the PR's templates while the key is present: use this only where everyone who can open a PR is trusted.

### Inputs

| Input | Default | Description |
|-------|---------|-------------|
| `helmfile-path` | `helmfile.yaml.gotmpl` | Entry helmfile, repo-relative |
| `render-subset` | `off` | Changed-file render subsetting: `off`, `shadow` or `on` (see below) |
| `full-render-label` | `atlas-full-render` | PR label that forces a full render for that PR |
| `diff-mode` | `dyff` | `dyff` for YAML-aware semantic diffs, `classic` for unified diffs |
| `sops-age-key` | | SOPS age private key for `*.values.sops.yaml` files (a secret) |
| `keyless` | `false` | Render without decrypting SOPS files on either side: secret values stay `ENC[...]` and are compared encrypted. A review without any key; structural changes to secrets stay visible, value changes show as changed ciphertext |
| `github-token` | `github.token` | Token for the comment and the merge-base lookup |
| `image` | derived | Review image. Derived from the action reference; required from a local path (`uses: ./`) |
| `checkout` | `true` | `false` when the workspace already holds the repository with both commits |
| `head`, `base` | | Two revisions to compare directly instead of the pull request (push-based reviews, manual runs) |
| `comment`, `comment-header` | `true`, `atlas-review` | Post the sticky comment; its marker (one comment per marker) |
| `fail-on-render-error` | `true` | Fail the step when the merge result does not render |
| `in-place` | `true` | Render in the checkout (faster on GitHub runners) or in a workspace under `RUNNER_TEMP` |
| `render-parallel`, `helmfile-timeout` | CPUs, `900` | Concurrent helmfile calls per render; seconds per call |
| `engine-repository` | `ghcr.io/max06/atlas` | Image repository of pinned engines: a side whose `atlas.yaml` pins another minor renders in `<engine-repository>:<pin>` (see [Reviews across engine versions](#reviews-across-engine-versions)) |

### Outputs

| Output | Description |
|--------|-------------|
| `status`, `has_changes` | `no-changes` or `changes`; `true` when manifests differ |
| `pr_status`, `baseline_status` | Render status of the merge result (`success`, `error`) and of the baseline (`success`, `error`, `missing`) |
| `render_mode`, `classify_mode` | `full` or `subset`; what the classifier decided |
| `changed_resources`, `changed_releases` | Counts |
| `shadow_result`, `shadow_missed` | `covered`, `uncovered`, `full`, `skipped` or `inconclusive`; deployments the classifier missed |
| `engine_baseline`, `engine_pr` | Who rendered each side: `self` or the image of its pin |
| `head`, `base` | The two commits |
| `comment_body`, `summary_file` | The comment markdown; the path of the job summary |

### Reviews across engine versions

Each side renders with the engine that its [`atlas.yaml`](#atlasyaml-the-engine-pin) pins. Thus a PR that changes the pin shows what the new engine changes in the rendered manifests.

- No `atlas.yaml`, or a pin of the action's own minor: the action renders the side itself.
- A pin of another minor: the action renders the side in the image `<engine-repository>:<pin>`. It starts the image as a sibling container through the docker socket of the runner, with the checkout and `RUNNER_TEMP` at their own paths and as the runner's user. The action mounts the socket into its container when the socket exists.
- The two talk through a request file and a result file (`atlas review ci side`, side contract 1). Releases before the contract cannot render a side: the review says so. If that is the baseline, the baseline counts as not rendered and the PR is not blocked; if it is the merge result, the review fails.
- `engine-repository` comes from the workflow file. With `pull_request_target` that is the base branch, so a PR selects only the version, not the source of the image.
- A build without a release version (a branch image) renders every side itself: it is the engine under test.
- When the pins differ, the comment says so, and the job summary lists the engine of each side.

`atlas review diff` on your machine renders both sides with one engine. If a side pins another minor than your `atlas`, it stops with an explanation. `--single-engine` compares anyway (content changes only).

### Migrating from the reusable workflow

`snapshot-review.yml` (`uses: max06/atlas/.github/workflows/snapshot-review.yml@…`) is deprecated and is removed in the next minor release. Replace the `uses:` job with the example above: the inputs keep their names (`helmfile-path`, `render-subset`, `full-render-label`, `diff-mode`), `runs-on` and `environment` move to your job, and the secret becomes the input `sops-age-key`.

### Changed-file render subsetting

The directory convention *is* the dependency graph, so a PR's changed paths select the `(cluster, deployment)` pairs whose render can differ. The workflow emits the discovery map of both revisions, classifies `git diff --name-status` against them, and — depending on the `render-subset` input — either renders only the selection or renders everything while checking the classifier:

| Changed path | Selected |
|---|---|
| `deployments/<prefix>/apps/<name>/**` | `<name>` on every cluster under `<prefix>` (global `apps/` → all clusters) |
| `deployments/<prefix>/<file>` (cluster/group values) | every deployment of every cluster under `<prefix>` |
| `deployments/<file>` (global values) | **full render** |
| `templates/<t>/**` | every deployment instantiating template `<t>` (`<t>` may span directories, e.g. `templates/apps/my-app`) |
| `templates/<ns>/<file>` outside any used template (a helper shared by `templates/<ns>/*`) | every deployment instantiating a template under `<ns>/` |
| `<chart>/**` for a local chart in the discovery map | every deployment whose releases render from `<chart>` |
| entry helmfile, anything else (unused charts, docs, …) | **full render** (default-deny) |
| pairs present on one revision only (new cluster, removed deployment) | always selected |

Templates are expected to read files only from their own directory and from ancestor directories inside the templates root. A template that reads another template's directory, or a file outside the templates root, is invisible to the classifier.

| `render-subset` | Behaviour |
|---|---|
| `off` (default) | Classic full render. |
| `shadow` | Full render; the classification is computed alongside and every changed release must lie inside the selection. A violation is reported in the comment as a warning — this is the evidence for cutting over. The check is only meaningful when both sides render completely. |
| `on` | Renders only the selection; the comment states the scope ("N of M deployments") and the rules applied. |

The PR label named by `full-render-label` (default `atlas-full-render`) forces a full render for that PR. A consumer pinned to an ATLAS version without `ATLAS_DISCOVERY_MAP` falls back to a full render automatically.

### Error handling

- **Target branch render errors** — reported as warnings but do not block merging (the PR may be the fix)
- **Merge-result render errors** — reported and fail the pipeline, with a local-reproduction command in the PR comment
- **Merge ref unavailable** — falls back to PR branch with a prominent warning (likely merge conflicts)
- A **job summary** on the pipeline run page shows which deployments were discovered on each side

### The CI key

If your repository uses SOPS-encrypted value files, the `sops-age-key` secret is **mandatory**. **Recommended: create a dedicated CI age key** rather than reusing a personal or production key:

```bash
age-keygen -o ci-key.txt
```

Add the public key as an additional recipient to your `.sops.yaml` and re-encrypt:

```bash
sops updatekeys deployments/global.values.sops.yaml
```

Store the private key as a GitHub repository secret named `SOPS_AGE_KEY`.
