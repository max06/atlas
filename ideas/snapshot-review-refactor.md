# ATLAS Review Workflow Refactor — Plan

## Status Quo

`snapshot-review.yml`: 1075 lines, single monolithic reusable workflow. Inline bash
handles tool installation, two-phase rendering (PR-first with sidedump, then baseline),
redaction-map replay, per-resource YAML splitting, line-level diff generation, comment
assembly with truncation, job summary, and error handling.

**Pain points:**
- Untestable — bash logic is inlined in YAML, can't run or assert on it locally
- Over-suppression — releases with no secrets still get their diffs hidden when the
  sidedump map file is absent (the plugin early-returns before writing the map)
- Security theater — merge-result pipeline runs untrusted PR code with SOPS key access
- Version check conflates two concerns (workflow version vs template version)
- Diff engine is 300+ lines of bash doing work that dyff does better

---

## Architecture Options

### Option A: Docker Container Actions

```
.github/
  actions/
    atlas-render/
      Dockerfile          # alpine + helm 4.x + helmfile 1.x + sops + yq + dyff + jq
      action.yml          # typed inputs/outputs
      render.sh           # helmfile list + template + sidedump wiring
    atlas-diff/
      Dockerfile          # alpine + yq + dyff + jq
      action.yml
      diff.sh             # replay + dyff + comment assembly
  workflows/
    snapshot-review.yml   # ~80 lines, thin orchestrator
```

Pre-built image pushed to `ghcr.io/max06/atlas-render:v1` and
`ghcr.io/max06/atlas-diff:v1` — action.yml references
`image: docker://ghcr.io/...` so runners pull instead of build.

| Dimension | Rating | Notes |
|-----------|--------|-------|
| Setup time for consumers | Excellent | Zero — tools baked into image |
| Testability | Excellent | Run container locally with test fixtures |
| Maintainability | Good | Scripts in separate files, version-tagged images |
| Debugging | Fair | Logs come from inside container |
| Infrastructure overhead | Moderate | Need GHCR publish workflow + image versioning |
| Migration effort | High | New Dockerfile + action.yml + publish pipeline |

### Option B: TypeScript Action

```
.github/
  actions/
    atlas-review/
      action.yml
      dist/index.js       # ncc-compiled bundle
      src/
        index.ts          # orchestrator
        render.ts          # exec(helmfile ...)
        diff.ts            # exec(dyff ...) + parse output
        comment.ts         # markdown assembly
        version-check.ts   # 3-way pin compare
```

Uses `@actions/core`, `@actions/exec`, `@actions/github` (Octokit). Still needs
helm/helmfile/sops/yq installed on the runner (via aqua or a setup composite action).

| Dimension | Rating | Notes |
|-----------|--------|-------|
| Type safety | Excellent | Full TS, testable with vitest |
| GitHub API integration | Excellent | Native Octokit, no sticky-comment action needed |
| Testability | Excellent | Unit tests for comment builder, integration for render |
| Tool installation | Still needed | aqua or composite action for CLI tools |
| Project fit | Poor | Introduces Node/TS to a gotmpl/YAML project |
| Migration effort | Very high | New language, build pipeline, learning curve |

### Option C: Composite Actions (bash, no Docker)

```
.github/
  actions/
    atlas-setup/action.yml    # aqua install of all tools
    atlas-render/action.yml   # render one side
    atlas-diff/action.yml     # replay + dyff + comment
  workflows/
    snapshot-review.yml        # ~80-100 lines
```

Same bash, just organized into action directories. Scripts referenced via
`${{ github.action_path }}/render.sh`. Each action has typed inputs/outputs.

| Dimension | Rating | Notes |
|-----------|--------|-------|
| Migration effort | Low | Move existing bash into action dirs |
| Testability | Fair | Can run scripts locally, but no container isolation |
| Setup time | Same as today | Tools installed per-run via aqua |
| Maintainability | Good | Smaller files, clear boundaries |
| Infrastructure | None | No build pipeline, no image registry |

### Option D: Hybrid — Docker for render+diff, composite for orchestration

The render and diff actions use Docker (tools baked in). The workflow and any
lightweight steps (checkout, comment posting) stay as plain workflow steps.

**This is the recommended approach.** Docker where the tool chain matters, YAML where
GitHub-native features matter (checkout, secrets, comment posting).

---

## Recommended Architecture (Option D — Hybrid)

### Workflow structure (~80 lines)

```yaml
name: ATLAS Review
on:
  workflow_call:
    inputs:
      helmfile-path: { type: string, default: "helmfile.yaml.gotmpl" }
    secrets:
      sops-age-key: { required: false }

permissions:
  contents: read
  pull-requests: write

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout merge result
        uses: actions/checkout@v6
        with:
          ref: refs/pull/${{ github.event.pull_request.number }}/merge
        id: merge-checkout
        continue-on-error: true

      - name: Fallback to PR branch
        if: steps.merge-checkout.outcome == 'failure'
        uses: actions/checkout@v6
        with:
          ref: ${{ github.event.pull_request.head.sha }}

      - name: Render PR side
        uses: max06/atlas/.github/actions/atlas-render@main
        id: pr
        with:
          helmfile-path: ${{ inputs.helmfile-path }}
          snapshot-label: pr
          sops-age-key: ${{ secrets.sops-age-key }}
          enable-sidedump: "true"

      - name: Checkout target branch
        uses: actions/checkout@v6
        with:
          ref: ${{ github.event.pull_request.base.ref }}
          clean: false

      - name: Render baseline
        uses: max06/atlas/.github/actions/atlas-render@main
        id: baseline
        with:
          helmfile-path: ${{ inputs.helmfile-path }}
          snapshot-label: baseline
          sops-age-key: ${{ secrets.sops-age-key }}
          enable-sidedump: "false"

      - name: Diff, replay, and build comment
        uses: max06/atlas/.github/actions/atlas-diff@main
        id: diff
        with:
          baseline-dir: ${{ steps.baseline.outputs.snapshot-dir }}
          pr-dir: ${{ steps.pr.outputs.snapshot-dir }}
          sidedump-map-dir: ${{ steps.pr.outputs.sidedump-dir }}
          workflow-pin-target: ${{ steps.baseline.outputs.workflow-pin }}
          workflow-pin-merge: ${{ steps.pr.outputs.workflow-pin }}

      - name: Post PR comment
        if: always() && steps.diff.outputs.comment-body
        uses: marocchino/sticky-pull-request-comment@v3.0.4
        with:
          header: atlas-review
          message: ${{ steps.diff.outputs.comment-body }}

      - name: Write job summary
        if: always()
        run: echo "${{ steps.diff.outputs.summary-body }}" >> "$GITHUB_STEP_SUMMARY"

      - name: Gate on PR render status
        if: always() && steps.pr.outputs.status == 'error'
        run: exit 1
```

### atlas-render action

**Dockerfile:**
```dockerfile
FROM alpine:3.21
RUN apk add --no-cache bash curl git jq
# Pin exact versions — renovate manages these
COPY install-tools.sh /install-tools.sh
RUN /install-tools.sh \
  helm=v4.1.3 \
  helmfile=v1.4.3 \
  sops=v3.9.4 \
  yq=v4.45.4
COPY entrypoint.sh /entrypoint.sh
COPY render.sh /render.sh
ENTRYPOINT ["/entrypoint.sh"]
```

`install-tools.sh` uses direct GitHub release URLs — no eget/aqua dependency in the
image itself. Versions are build args so Renovate can bump them.

**action.yml inputs:**
- `helmfile-path` — entry point
- `snapshot-label` — "pr" or "baseline" (used for output dir naming)
- `sops-age-key` — optional secret
- `enable-sidedump` — "true"/"false"

**action.yml outputs:**
- `status` — success/error/missing
- `snapshot-dir` — path to rendered output
- `sidedump-dir` — path to captured redaction maps (only when enable-sidedump=true)
- `workflow-pin` — detected workflow ref from caller's workflow file
- `list-json` — path to helmfile list output (for job summary)
- `filter-supported` — whether --selector filtering worked

**Core logic (render.sh, ~80 lines):**
1. Configure SOPS key file if provided
2. `helmfile list --output json` → discover deployments
3. Selector-support probe (same as today, but cleaner)
4. Render via filter-fast-path or bulk fallback
5. Detect workflow pin from `.github/workflows/*.y*ml`
6. Write outputs

### atlas-diff action

**Dockerfile:**
```dockerfile
FROM alpine:3.21
RUN apk add --no-cache bash jq
COPY install-tools.sh /install-tools.sh
RUN /install-tools.sh yq=v4.45.4 dyff=v1.12.0
COPY entrypoint.sh /entrypoint.sh
COPY replay.sh /replay.sh
COPY diff.sh /diff.sh
COPY comment.sh /comment.sh
ENTRYPOINT ["/entrypoint.sh"]
```

Lighter image — no helm/helmfile/sops needed for diffing.

**action.yml inputs:**
- `baseline-dir`, `pr-dir`, `sidedump-map-dir`
- `workflow-pin-target`, `workflow-pin-merge`
- `latest-atlas-release` (optional, fetched internally if not provided)
- `merge-fallback` — "true" if merge ref was unavailable

**action.yml outputs:**
- `comment-body` — full markdown for PR comment
- `summary-body` — full markdown for job summary (untruncated)
- `status` — no-changes/changes/error
- `total-changes`, `total-releases`

**Core logic:**

1. **replay.sh (~60 lines):** Same scrub-baseline logic, but with a fix: handle empty
   map files as "no secrets" (see Redaction Improvements below).

2. **diff.sh (~100 lines):** Per-release diff using dyff:
   ```bash
   # For each release, concatenate YAML into multi-doc streams
   find "$BASELINE_DIR/$release" -name '*.yaml' | sort | xargs cat > /tmp/baseline.yaml
   find "$PR_DIR/$release" -name '*.yaml' | sort | xargs cat > /tmp/pr.yaml
   # dyff with GitHub markdown output
   dyff between /tmp/baseline.yaml /tmp/pr.yaml \
     --output github --detect-kubernetes --set-exit-code
   ```
   dyff handles: key reordering, type-aware comparison, Kubernetes resource detection,
   rename detection, and markdown formatting. Replaces ~200 lines of current bash
   (split_resources, per-resource diff loop, markdown assembly, truncation).

3. **comment.sh (~80 lines):** Assembles the final comment from:
   - Version check results (see below)
   - Security warnings
   - Per-release dyff output blocks
   - Suppression notices for unsafe releases
   - Truncation handling for 65KB comment limit (full output in summary-body)

### Pre-built images on GHCR

A separate workflow in max06/atlas builds and pushes images on release:

```yaml
# .github/workflows/build-actions.yml
on:
  push:
    tags: ['v*']
    paths: ['.github/actions/**']
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v6
        with:
          context: .github/actions/atlas-render
          push: true
          tags: ghcr.io/max06/atlas-render:${{ github.ref_name }},ghcr.io/max06/atlas-render:latest
      - uses: docker/build-push-action@v6
        with:
          context: .github/actions/atlas-diff
          push: true
          tags: ghcr.io/max06/atlas-diff:${{ github.ref_name }},ghcr.io/max06/atlas-diff:latest
```

Action.yml references `image: docker://ghcr.io/max06/atlas-render:latest` for @main,
or `docker://ghcr.io/max06/atlas-render:v0.3.0` for tagged releases. Consumers pulling
`@main` get latest image; consumers pinning `@v0.3.0` get that image.

---

## Version Check Redesign

Split into two independent checks with clear, distinct messaging:

### Check 1: Workflow version (security-relevant)

The workflow itself runs from `max06/atlas/.github/workflows/snapshot-review.yml@<ref>`.
This ref determines which *review pipeline logic* runs — security fixes, redaction
improvements, etc.

| Scenario | Message |
|----------|---------|
| `@main` | Silent (ideal — always gets security fixes) |
| `@latest-tag` | Note: "Workflow pinned to `@v0.3.0`. You are responsible for updating to receive security fixes. Consider using `@main` to get fixes automatically." |
| `@old-tag` | Warning: "Workflow pinned to `@v0.2.0`, latest is `@v0.3.0`. Update to receive security and redaction improvements. Using `@main` is recommended." |
| Not detected | Silent (don't false-alarm on renamed/forked workflows) |

### Check 2: ATLAS template version (compatibility-relevant)

The ATLAS version is what the consumer's `helmfile.yaml.gotmpl` references — the
templates that generate Kubernetes manifests. This is NOT the workflow ref.

Detection: parse the consumer's helmfile for the ATLAS ref (e.g., from a git
submodule, a `ref:` field, or the directory structure). If not detectable, skip.

| Scenario | Message |
|----------|---------|
| Latest release | Silent |
| Behind latest | Note: "ATLAS `v0.2.0` is available (you're on `v0.1.0`). See release notes for changes." |
| `@main` | Warning: "`main` may contain unreleased breaking changes. Pin to a release tag for production use." |
| Not detected | Silent |

### Implementation

The atlas-render action detects the workflow pin (already exists) and the ATLAS
template version (new — needs a convention for detection). The atlas-diff action
receives both and builds the messaging.

---

## Security Model

### Current threat: untrusted code + SOPS key

The merge-result pipeline checks out PR code and runs `helmfile template` with the SOPS
age key. A malicious PR could modify gotmpl templates to exfiltrate the key via network
calls, file writes, or stderr smuggling.

### Mitigation options (ordered by effort)

#### 1. Document the trust boundary (low effort, do first)

Add explicit guidance to the README and workflow comment header:

> **Security model:** This workflow executes helmfile templates from the PR branch with
> access to the SOPS decryption key. Only run this workflow on PRs from trusted
> contributors. For repositories that accept external contributions, configure
> [environment protection rules](https://docs.github.com/en/actions/deployment/targeting-different-environments/using-environments-for-deployment#environment-protection-rules)
> to require reviewer approval before the workflow runs.

Most ATLAS consumers are private repos with trusted contributors. This covers 90% of
cases.

#### 2. Environment protection rules (medium effort)

The workflow job references a protected environment:

```yaml
jobs:
  review:
    environment: atlas-review  # requires approval for fork PRs
```

Org admins configure the environment to require a reviewer before the job runs.
Fork PRs pause until a maintainer approves. Same-repo PRs from branch protection
pass-listed users run immediately.

#### 3. Two-phase pipeline (high effort, future)

Split into two workflows:

**Phase 1** (`pull_request`, no secrets):
- Checkout PR code
- Render PR manifests WITHOUT SOPS decryption (values stay encrypted or use placeholders)
- Upload rendered output as artifact

**Phase 2** (`workflow_run`, has secrets):
- Download PR artifact (untrusted rendered YAML only — no code execution)
- Render baseline with SOPS key
- Diff the two sides
- Post comment

**Challenge:** helmfile template references SOPS values via `fetchSecretValue`. Without
the key, the render fails. Options:
- Stub the SOPS values with a known placeholder during Phase 1, then redact those
  placeholders in Phase 2
- Use `ATLAS_REDACT_SECRETS=true` which already replaces values with type-aware
  placeholders — but this still calls `fetchSecretValue` which needs the key

**Verdict:** Phase 3 is architecturally clean but requires ATLAS template changes to
support keyless rendering. Defer to a future version. Start with options 1 + 2.

---

## Diff Engine — dyff Integration

### What dyff gives us

- **YAML-aware:** compares by document path (`spec.containers[0].image`), not line
- **Kubernetes-aware:** `--detect-kubernetes` groups changes by resource
- **Rename detection:** `--detect-renames` catches renamed resources
- **GitHub markdown:** `--output github` produces comment-ready markdown
- **Multi-doc:** handles `---`-separated YAML streams natively

### Integration approach

For each release pair:

```bash
# Concatenate all manifests per release into multi-doc streams
cat_yamls() {
  find "$1" -name '*.yaml' -type f | sort | xargs cat
}

cat_yamls "$BASELINE_DIR/$release" > /tmp/base.yaml
cat_yamls "$PR_DIR/$release" > /tmp/pr.yaml

# dyff produces GitHub-flavored markdown
RELEASE_DIFF=$(dyff between /tmp/base.yaml /tmp/pr.yaml \
  --output github \
  --detect-kubernetes \
  --omit-header \
  --set-exit-code 2>/dev/null) || true
```

### What this replaces

| Current bash | dyff equivalent |
|--------------|-----------------|
| `split_resources()` (yq split per resource) | Not needed — dyff handles multi-doc |
| Per-resource `diff -uN` loop | Single `dyff between` call per release |
| Manual markdown assembly | `--output github` |
| Key reordering noise | Ignored by default (YAML-semantic compare) |
| ~200 lines of bash | ~15 lines |

### What stays

- Release-path walking (discover which releases exist on each side)
- Bucketing logic (added/removed/modified/suppressed)
- Truncation for 65KB comment limit (dyff doesn't know about GitHub limits)
- Comment header/footer assembly

### Considerations

- dyff output format may not match current comment layout exactly — accept the visual
  change as an improvement (path-based diffs are more reviewable than line diffs)
- Test with real ATLAS output to validate dyff handles our manifest shapes well
- dyff binary is ~15MB — acceptable for a Docker image

---

## Redaction Improvements

### Bug: over-suppression of no-secret releases

**Root cause:** `atlas-redact.sh` line 37-39 early-returns when `REPL_B64` is empty
(no replacements), skipping the side-dump write at line 61-69. Releases with no secrets
therefore have no map file. The diff step treats missing map files as "unsafe — suppress."

**Fix:** Write an empty map file even when there are no replacements:

```bash
# In atlas-redact.sh, before the early-return for empty REPL_B64:
if [ -z "$REPL_B64" ]; then
  # Side-dump an empty map so the review pipeline knows this release
  # was processed and has no secrets (vs "old ATLAS, can't determine").
  if [ -n "$MAP_DUMP_PATH" ]; then
    MAP_DUMP_DIR="$(dirname "$MAP_DUMP_PATH")"
    mkdir -p "$MAP_DUMP_DIR" 2>/dev/null || true
    echo '{}' > "$MAP_DUMP_PATH" 2>/dev/null || true
  fi
  printf '%s' "$INPUT"
  exit 0
fi
```

**Diff step change:** Treat empty map file (or `{}` content) as "no secrets, safe to
diff" — show full content diff. Only suppress when no map file exists at all.

```bash
# In the diff bucketing logic:
if [ -f "$map_file" ]; then
  MAP_SIZE=$(stat -c%s "$map_file" 2>/dev/null || echo 0)
  if [ "$MAP_SIZE" -le 3 ]; then  # {} + newline = 3 bytes
    # Empty map — release has no secrets, safe to show full diff
    BUCKET="safe"
  else
    # Non-empty map — secrets were scrubbed, show scrubbed diff
    BUCKET="scrubbed"
  fi
else
  # No map file — old ATLAS or removed release, suppress
  BUCKET="no-map"
fi
```

### Future: eliminate the replay pipeline entirely

If all consumers are on ATLAS >= v0.2.0 (which has inline redaction), the replay step
is unnecessary — both sides are already redacted. The replay pipeline exists solely to
support baselines rendered by ATLAS v0.1.0. Once v0.1.0 is EOL:

1. Remove scrub-baseline.sh
2. Remove sidedump wiring from atlas-redact.sh
3. Remove replay step from workflow
4. Show full diffs for all releases (both sides already clean)

Track v0.1.0 usage via the selector-probe or a dedicated version-detection mechanism.

---

## Tool Installation

### In Docker actions (recommended)

Tools are baked into the image. `install-tools.sh` downloads from GitHub releases:

```bash
#!/bin/sh
set -e
for spec in "$@"; do
  tool="${spec%%=*}"
  version="${spec#*=}"
  case "$tool" in
    helm)     URL="https://get.helm.sh/helm-${version}-linux-amd64.tar.gz" ;;
    helmfile) URL="https://github.com/helmfile/helmfile/releases/download/${version}/helmfile_${version#v}_linux_amd64.tar.gz" ;;
    sops)     URL="https://github.com/getsops/sops/releases/download/${version}/sops-${version}.linux.amd64" ;;
    yq)       URL="https://github.com/mikefarah/yq/releases/download/${version}/yq_linux_amd64" ;;
    dyff)     URL="https://github.com/homeport/dyff/releases/download/${version}/dyff_${version#v}_linux_amd64.tar.gz" ;;
  esac
  # download, extract, install to /usr/local/bin
done
```

Renovate manages version bumps via regex in the Dockerfile or install script.

### Alternative: aqua (if staying with composite actions)

aqua has a proper GitHub Action (`aquaproj/aqua-installer`), a registry of 4000+ tools
(including helm, helmfile, sops, yq), and Renovate integration. One step replaces
Azure/setup-helm + helmfile/helmfile-action + nhedger/setup-sops:

```yaml
- uses: aquaproj/aqua-installer@v3
  with:
    aqua_version: v2.46.0
- run: aqua install  # reads aqua.yaml with pinned versions
```

**Verdict:** aqua is the right choice if we stay with composite actions. Docker image
baking is the right choice if we go Docker. No need for eget — aqua is strictly better
(action, registry, Renovate, checksums).

---

## Migration Path

### Phase 1: Quick wins (can do now, independent of architecture)

1. **Fix over-suppression bug** — write empty map file in atlas-redact.sh
2. **Split version checks** — separate workflow-version from template-version messaging
3. **Document security model** — add trust boundary guidance to README

### Phase 2: Docker actions (the main refactor)

1. Create `.github/actions/atlas-render/` with Dockerfile + action.yml + render.sh
2. Create `.github/actions/atlas-diff/` with Dockerfile + action.yml + diff.sh + replay.sh + comment.sh
3. Integrate dyff into atlas-diff
4. Rewrite snapshot-review.yml as thin orchestrator
5. Set up GHCR publish workflow for action images
6. Test on experimental branch with dummy consumer repo

### Phase 3: Security hardening (after Phase 2 works)

1. Add environment protection rule support to workflow
2. Document fork-PR security guidance
3. (Future) Investigate two-phase pipeline for keyless PR rendering

### Phase 4: Cleanup

1. Remove scrub-baseline.sh from plugin directory (after v0.1.0 EOL)
2. Remove selector-probe compat code (after all consumers on current helmfile)
3. Consider Go binary for diff/comment if bash scripts grow again

---

## Testing Strategy

Testing is the prerequisite — write tests before rewriting any logic.

### Layer 1: actionlint (static analysis)

Run `actionlint` in CI on every push to catch workflow YAML errors, expression type
mismatches, and script injection risks. Fast (~2s), no execution needed.

For `action.yml` schema validation, use `mpalmer/action-validator` or validate against
the SchemaStore JSON schema (`github-action.json`).

### Layer 2: bats-core (unit tests for action scripts)

We already have 354+ bats tests and the infrastructure. The action scripts (`render.sh`,
`diff.sh`, `replay.sh`, `comment.sh`) are pure bash that read inputs from env vars and
write to `$GITHUB_OUTPUT`. Test them the same way:

```bash
# tests/bats/workflow/diff.bats
setup() {
  load 'helpers/render'
  export GITHUB_OUTPUT="$(mktemp)"
  export GITHUB_STEP_SUMMARY="$(mktemp)"
}

@test "diff.sh detects no changes for identical renders" {
  # Use existing test fixtures — render once, copy, diff against self
  run bash "$(_repo_root)/.github/actions/atlas-diff/diff.sh" \
    "$SNAPSHOT_DIR" "$SNAPSHOT_DIR" ""
  [ "$status" -eq 0 ]
  grep -q 'status=no-changes' "$GITHUB_OUTPUT"
}

@test "diff.sh shows full diff for release with empty map (no secrets)" {
  echo '{}' > "$MAP_DIR/cluster1/deployment1/release1.json"
  run bash "$(_repo_root)/.github/actions/atlas-diff/diff.sh" \
    "$BASELINE_DIR" "$PR_DIR" "$MAP_DIR"
  [ "$status" -eq 0 ]
  # Should NOT be suppressed
  [[ "$output" != *"suppressed"* ]]
}

@test "diff.sh suppresses release with missing map file" {
  # No map file at all — old ATLAS
  run bash "$(_repo_root)/.github/actions/atlas-diff/diff.sh" \
    "$BASELINE_DIR" "$PR_DIR" "$EMPTY_MAP_DIR"
  [ "$status" -eq 0 ]
  [[ "$output" == *"no-map"* ]]
}

@test "comment.sh produces valid markdown under 65KB" {
  run bash "$(_repo_root)/.github/actions/atlas-diff/comment.sh" ...
  [ "$status" -eq 0 ]
  [ "${#output}" -lt 65000 ]
}
```

**Test categories for the workflow scripts:**
- `tests/bats/workflow/render.bats` — helmfile invocation, SOPS wiring, sidedump,
  selector probe, workflow pin detection
- `tests/bats/workflow/replay.bats` — scrub-baseline with empty maps, non-empty maps,
  missing maps, malformed maps
- `tests/bats/workflow/diff.bats` — dyff integration, release bucketing, added/removed/
  modified detection, truncation at 55KB
- `tests/bats/workflow/comment.bats` — version check messaging, security banners,
  markdown structure, edge cases (both sides error, baseline missing, etc.)
- `tests/bats/workflow/version-check.bats` — workflow pin scenarios, template version
  scenarios, messaging matrix

### Layer 3: Docker integration tests

Build the action image locally, run it with `docker run` and simulated GitHub env vars:

```bash
# tests/docker/test-render.sh
docker build -t atlas-render-test .github/actions/atlas-render/
docker run --rm \
  -v "$(pwd):/github/workspace" \
  -e GITHUB_OUTPUT=/tmp/output \
  -e GITHUB_WORKSPACE=/github/workspace \
  -e INPUT_HELMFILE_PATH=tests/helmfile.yaml.gotmpl \
  -e INPUT_SNAPSHOT_LABEL=pr \
  -e INPUT_ENABLE_SIDEDUMP=true \
  atlas-render-test
```

This validates: Dockerfile builds, tools are installed at correct versions, entrypoint
wiring works, workspace mounting works.

### Layer 4: Self-testing workflow

A workflow in the ATLAS repo itself that tests the actions on every push:

```yaml
# .github/workflows/test-actions.yml
name: Test Actions
on: [push, pull_request]
jobs:
  test-render:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: ./.github/actions/atlas-render   # local action reference
        id: render
        with:
          helmfile-path: tests/helmfile.yaml.gotmpl
          snapshot-label: test
          enable-sidedump: "true"
      - name: Verify render output
        run: |
          [ "${{ steps.render.outputs.status }}" = "success" ]
          [ -d "${{ steps.render.outputs.snapshot-dir }}" ]
          # Spot-check: known deployment should be in output
          ls "${{ steps.render.outputs.snapshot-dir }}/cluster1/deployment1/"
```

Uses `uses: ./` — no need for a separate consumer repo during development.

### Layer 5: Cross-repo smoke test (manual gate)

Before releasing: push to experimental branch, point the `atlas-template` consumer repo
at `uses: max06/atlas/.github/actions/atlas-render@experimental`, open a PR, verify the
comment renders correctly. This is the final acceptance gate, not automated.

### What NOT to test with act

`nektos/act` (~70k stars, mature) can run workflows locally but has significant
limitations with Docker container actions (Docker-in-Docker), reusable workflows
(edge-case bugs with nested calls), and runner environment fidelity. Use it for quick
local iteration on the thin orchestrator YAML, but don't rely on it as a correctness
gate. The bats + Docker integration layers give better coverage with less friction.

---

## dyff Sample Output

Tested with dyff 1.12.0 on representative Kubernetes manifests. The `--output github`
format produces clean, path-based diffs:

```
@@ spec.replicas @@
# apps/v1/Deployment/production/my-app
! ± value change
- 3
+ 5

@@ spec.template.spec.containers.my-app.image @@
# apps/v1/Deployment/production/my-app
! ± value change
- registry.example.com/my-app:v1.0.0
+ registry.example.com/my-app:v1.1.0

@@ spec.template.spec.containers.my-app.env @@
# apps/v1/Deployment/production/my-app
! + one list entry added:
+ - name: FEATURE_FLAG
+   value: enabled

@@ (root level) @@
# v1/Secret/default/old-secret
! - one document removed:
- apiVersion: v1
- kind: Secret
- ...

@@ (root level) @@
# v1/ConfigMap/default/extra-config
! + one document added:
+ apiVersion: v1
+ kind: ConfigMap
+ ...
```

**Advantages over current `diff -uN`:**
- Changes shown by YAML path, not line number — instantly tells you what changed
- Kubernetes resource identity in every block header (kind/namespace/name)
- Added/removed documents clearly labeled
- Key reordering and whitespace changes ignored (no noise)
- List entry additions shown semantically, not as line insertions

**Limitations to handle:**
- No built-in truncation — we still need to manage the 65KB comment limit
- No directory diffing — wrapper concatenates YAML files per release before calling dyff
- Output size can grow large for bulk changes — `--omit-header` and per-release wrapping
  in `<details>` blocks keeps the comment manageable

---

## ATLAS Template Version Detection

Helmfile's lockfile (`helmfile.lock`) only tracks Helm chart versions — no sub-helmfile
references, no git submodule versions. Dead end for detecting ATLAS version.

**Viable approaches:**

1. **Git submodule status** — if consumer uses ATLAS as a submodule, `git submodule
   status` returns the pinned commit SHA. Map SHA to tag via `git describe --tags`.
   Works automatically for submodule-based consumers.

2. **Explicit version file** — convention: consumer creates `.atlas-version` containing
   the pinned ref (tag or SHA). The render action reads it. Requires consumer opt-in.

3. **Sub-helmfile ref parsing** — parse the consumer's `helmfile.yaml.gotmpl` for the
   ATLAS include path, resolve the ref from the directory or submodule. Most accurate
   but fragile (depends on include syntax).

**Recommendation:** Start with git submodule detection (automatic, no consumer action
needed). Fall back to `.atlas-version` file if present. If neither is found, skip the
template version check silently.

---

## Replay Pipeline — Kept Permanently

Per discussion: the replay pipeline stays as a permanent safety net. New ATLAS features
could introduce new secret-leak vectors at any time. The sidedump+replay architecture
catches these at the review layer even if the template-level redaction misses something.

The over-suppression fix (empty map for no-secret releases) and the dyff integration
make the replay pipeline lighter and more accurate without removing it.

---

## Revised Migration Path

### Phase 0: Testing foundation (do first)

1. Extract current inline bash into standalone scripts (no behavior change)
2. Write bats tests for each script against existing test fixtures
3. Add `actionlint` to CI
4. Verify all scripts pass tests identically to the current workflow behavior

### Phase 1: Quick wins (independent of architecture)

1. **Fix over-suppression bug** — write empty map file in atlas-redact.sh
2. **Split version checks** — separate workflow-version from template-version messaging
3. **Document security model** — add trust boundary guidance to README

### Phase 2: Docker actions (the main refactor)

1. Create `.github/actions/atlas-render/` with Dockerfile + action.yml + render.sh
2. Create `.github/actions/atlas-diff/` with Dockerfile + action.yml + diff.sh + replay.sh + comment.sh
3. Integrate dyff into atlas-diff
4. Rewrite snapshot-review.yml as thin orchestrator (~80 lines)
5. Set up GHCR publish workflow for action images
6. Add self-testing workflow (`uses: ./`)
7. Test on experimental branch with `atlas-template` consumer repo

### Phase 3: Security hardening

1. Add environment protection rule support to workflow
2. Document fork-PR security guidance
3. (Future) Investigate two-phase pipeline for keyless PR rendering

### Phase 4: Polish

1. Remove selector-probe compat code (after all consumers on current helmfile)
2. Add template-version detection (git submodule → .atlas-version fallback)
3. Consider Go binary for diff/comment if bash scripts grow again

---

## Open Questions

1. **GHCR image versioning strategy?** Tag per ATLAS release? Separate image version?
   Recommend: same tag as ATLAS release, plus `:latest` for `@main` consumers.

2. **Phase 0 scope?** Extract scripts first (same behavior, testable) or jump straight
   to Docker actions? Extracting first gives us a safety net; jumping saves a migration
   step. Recommend: extract + test first, then migrate to Docker.

3. **dyff format acceptance?** Tentatively yes — test with real ATLAS output before
   committing. The demo output looks good; need to verify with multi-chart releases,
   patched resources, and large manifests.

4. **Environment protection rules — default on?** Should the workflow YAML include
   `environment: atlas-review` by default, or leave it as opt-in documentation?
   Default-on means consumers need to create the environment before the workflow works.
   Recommend: opt-in, documented clearly.
