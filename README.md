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
