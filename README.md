# ATLAS

**Application Topology Layer Across Systems**

Navigate your GitOps deployments across any cluster topology with hierarchical inheritance.

**Disclaimer:**
- **Not** created by AI!
- Claude and grok (their web-versions) were used to save some time finding logical errors and to create code comments.
- No "agent" ever had direct access to the code, or was left unattended.

And I prefer to keep it that way.

# **THIS IS WORK IN PROGRESS - DO NOT USE**

---

## Overview

ATLAS is a pre-configured Helmfile made to automatically discover clusters in your GitOps repository and assigns applications to them using a hierarchical inheritance model. Define applications once at the global level, override at the group level, or specify at the cluster level — ATLAS handles the rest.

Perfect for managing:
- Multi-cluster Kubernetes environments
- ArgoCD ApplicationSets
- Fleet deployments
- Any GitOps workflow with hierarchical cluster organization

### Key Features

- 🌳 **Hierarchical Inheritance** - Global → Group → Cluster deployment assignment
- 🔍 **Automatic Discovery** - No manual cluster registration required
- 📦 **Pure Helmfile** - Only uses functions included in helmfile, no additional dependencies
- 🎯 **Flexible Structure** - Support for standalone clusters and cluster groups
- 🚀 **GitOps Native** - Designed for declarative infrastructure

---

## Value Inheritance Logic

ATLAS loads and merges values from six levels. Later sources override earlier ones using deep merge (`mergeOverwrite`).

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

**Template-include** and **template-defaults** are both entries in the Helmfile release's `values:` list inside an app template's `helmfile.yaml.gotmpl`. The list is ordered — the **last element has highest priority**, regardless of whether it is a file path (string) or an inline map. The relative priority of includes vs defaults depends on their position in the list; the table above shows the conventional ordering.

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
3. **Standalone clusters skip group level**: A cluster path without `/` (e.g., `cluster1`) has no group; group-level files are not loaded.
4. **Templated values have access to prior values**: `.yaml.gotmpl` files are rendered with all previously loaded values as template context, enabling computed values that reference earlier layers. This applies to both template-include files and hierarchy-level gotmpl files.
5. **Atlas context is always present**: The `atlas` key in the merged values always contains deployment metadata (`cluster`, `deploymentName`, `deploymentPath`) and is not overridden by value files.
6. **Template values list is ordered**: Within an app template's release `values:` list, items are processed in order — last entry wins. Both file references (strings) and inline maps follow this rule.
7. **ATLAS hierarchy overrides template values**: The hierarchy values (global → group → cluster → deployment) are applied after the template's own values, ensuring deployment-specific configuration always wins over app template defaults.
