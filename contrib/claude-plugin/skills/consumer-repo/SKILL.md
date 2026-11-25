---
name: consumer-repo
description: Working in an ATLAS consumer repository, a GitOps repo with an `atlas.yaml` (`kind: Root`) at its root, deployment definitions (`<cluster>/apps/<name>/deployment.yaml`) and app templates (`<templates>/<name>/helmfile.yaml.gotmpl`). Use before adding or changing an app template, a deployment, hierarchy values or SOPS values files, and to render, review or debug such a change with the atlas CLI (`bin/atlas`).
---

# Working in an ATLAS consumer repository

ATLAS discovers clusters in a directory tree and turns every (cluster,
deployment) pair into Helm releases, with values inherited global → group →
cluster → deployment. Helmfile does the rendering; Argo CD renders the same way
through the ATLAS config management plugin. This skill is the operating guide
for a repository that *uses* ATLAS. The full reference is the ATLAS README:
https://github.com/max06/atlas/blob/main/README.md (it describes `main`; the
repository pins an engine version, see below).

Read the repository's own `CLAUDE.md` too: clusters, ingress and secret
conventions, and cluster access are local decisions.

## Orientation

1. `atlas.yaml` at the root:
   - `spec.engine` is the pinned ATLAS version (the `bin/atlas` image, the Argo CD plugin, the PR review).
   - `spec.templateDir` is where app templates live (default `templates`).
   - `spec.deploymentDir` is where the cluster tree lives (default `deployments`).
   - Features newer than the pin don't exist here, even if the README describes them.
2. `bin/atlas discover` prints the discovery map: every (cluster, deployment) with the templates and local charts it uses.
3. Copy the conventions of a neighbouring app before inventing your own.

## The model in brief

- **Cluster:** a directory that contains `apps/`. Its parent directory is the **group**. Leaf cluster names are unique, and `-c` takes the leaf name or the full path.
- **Deployment:** `<cluster>/apps/<name>/deployment.yaml`. A group- or top-level `apps/` applies the deployment to every cluster below it.

  ```yaml
  apps:
    - template: my-app          # <templateDir>/my-app/helmfile.yaml.gotmpl
      name: primary             # optional: several instances of one template
      namespace: production     # optional, wins over settings.namespace
      values:                   # optional, instance values (highest plain layer)
        - replicas: 2
      secrets:                  # optional, SOPS files, merged last
        - secrets.sops.yaml
  settings: {}                  # read by the repo's Argo CD ApplicationSet: see its CLAUDE.md
  ```

- **App template:** a plain helmfile `releases:` list. Write it as vanilla helmfile. ATLAS rewrites each release:
  - Release names get the instance name (`apps[].name`, `nameStyle` prefix/suffix).
  - Relative chart and patch paths resolve against the template directory.
  - `condition` and `installed` are stripped: a deployment exists because its `deployment.yaml` exists.
  - `values` is replaced by the hierarchy loader.
  - `postRenderer` is rejected: ATLAS owns that slot.
  - `skipSchemaValidation` defaults to true.
- **Values**, lowest to highest priority:
  1. chart defaults
  2. the template's `values:` list (last entry wins)
  3. the template's `release.secrets`
  4. `global.values.*`
  5. `<group>/group.values.*`
  6. `<cluster>/cluster.values.*`
  7. `<deployment dir>/values.*`
  8. `apps[].values`
  9. `apps[].secrets`

  Within one level, `.sops.yaml` < `.yaml` < `.yaml.gotmpl`. Merges are deep (`mergeOverwrite`), and a missing file is skipped. `.yaml.gotmpl` files see every value loaded before them.
- **Template context:** use `{{ .Values.x }}` (the bare `{{ .x }}` works but breaks when copied into helm). Deployment metadata is under `.Values.atlas.deployment.*`: `cluster`, `clusterName`, `clusterGroup`, `deploymentName`, `deploymentPath`. Instance metadata is under `.Values.atlas.instance.*`: `template`, `name`.
- **Namespace precedence:** the template release's `namespace` < `settings.namespace` < `apps[].namespace`.

## The CLI: `bin/atlas`

`bin/atlas` is a wrapper that runs the CLI image named by `spec.engine` in Docker (helm, helmfile, kustomize and sops included). It mounts the repository at its own path, so it works from a git worktree, and it mounts output paths outside the repository too.

| Command | Use it for |
|---|---|
| `bin/atlas discover` | the map of clusters, deployments, templates |
| `bin/atlas render -c <cluster> -d <deployment> -o <dir>` | manifests of one pair, one directory per release: `<dir>/<cluster path>/<deployment>/<release>/` (stdout without `-o`) |
| `bin/atlas inspect -c <cluster> -d <deployment> [--values]` | the helmfile states ATLAS generates, with the merged values: where a value comes from, without helm |
| `bin/atlas review diff [--head <rev>] [--patch]` | what merging the change does: renders the affected pairs on the target branch and on the merge result, lists the releases that differ |
| `bin/atlas review diff --format markdown` | the comment the CI review posts |
| `bin/atlas review classify` | which pairs a change can affect, without rendering |
| `bin/atlas doctor` | tool versions |

Notes:
- **Keyless by default.** Without `--decrypt`, `--redact` or `--argocd`, SOPS values stay `ENC[...]` and no key is needed. Don't pass those flags.
  - A chart that validates a secret's length or format can fail on `ENC[...]` strings. That is an artifact of the keyless render, not a bug in the change.
- **`review` never fetches.** Its base is `origin/<default branch>` as the clone has it, so run `git fetch` first. The default head is the working tree, uncommitted and untracked files included; `--head origin/<branch>` reviews a pushed branch instead.
- **Rendered values embed absolute paths.** Compare renders made from the same path, which is why `review` renders both sides in one place.
- Read rendered YAML with `yq`. To pick one resource: `yq 'select(.kind=="ConfigMap" and .metadata.name=="x")' file`.

## Making a change

1. **Leave the user's working tree alone.** Work in a branch from an up-to-date `origin/<default branch>`, in a `git worktree` if the checkout has uncommitted changes.
2. **Edit.** Comment non-obvious decisions in the template: the next reader is a human or another agent without your context.
3. **Render the pair** you changed and check the resources you meant to change: names, namespaces, values, checksum annotations that should roll a pod.
4. **Review:** `git fetch && bin/atlas review diff --head <your branch or nothing>`. Only the pairs you expect should show up. A pair you didn't expect means shared values or a shared template reach further than you thought.
5. **Commit and open a PR.** The CI review renders the same diff.
6. **After the merge,** someone syncs the deployment in Argo CD if it doesn't sync on its own. Check the result in the cluster if the repository's `CLAUDE.md` says how.

## Pitfalls

- **Every `.gotmpl` file is a Go template, comments included.** A literal `{{ … }}` meant for a later engine breaks the render or gets evaluated too early. This covers Argo CD goTemplate, Helm `tpl`, Pebble (Kestra), Jinja and the like.
  - Typical error: `function "<name>" not defined`, naming a function of the other engine (for example `secret` for a Pebble comment).
  - Escape it: ``{{`{{ .values.x }}`}}``. Or keep such files as plain `.yaml`, outside helmfile's reach.
- **Plain `.yaml` hierarchy files are not templated;** only `.yaml.gotmpl` is.
- **`range` over a `dict` walks the keys in sorted order,** not in the order written.
- **A Go template `eq` with mixed types fails or never matches:** YAML `true` is a bool, `"true"` a string. Normalise with `toString` when a value can be either.
- **Per-instance release names only change when `apps[].name` differs from the template name.** Don't add `{{ .Values.atlas.instance.name }}` to release names yourself.

## Secrets

- **Where they live:** encrypted values go in `*.values.sops.yaml` at any hierarchy level, a deployment's `values.sops.yaml`, or files in `secrets:` lists. Templates read them like any other value.
- **Never decrypt, print or write a secret in clear text,** and never commit a plaintext one.
  - For a new secret: name the key path, reference it in the template, and ask the user to fill it in with `sops edit <file>`.
  - Until they do, a keyless render shows the key missing or as `ENC[...]`.
- **Keep identifying details out of plain files,** such as domains, addresses and account names. Take them from hierarchy values (often encrypted global values), as the repository's existing templates do.
