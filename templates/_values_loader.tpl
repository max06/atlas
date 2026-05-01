{{- /*
# code: language=helm

============================================================
atlas.values.merged — Stage-3 shared values resolver
============================================================

Single source of truth for the values that reach a stage-3 release. Used
in two places:

  1. helmfile.values-loader.yaml.gotmpl  — invoked by helmfile at
     release-evaluation time (the actual values: file in the release).
     Called with redact=false; emits the real merged YAML for helm.

  2. helmfile.instance.yaml.gotmpl  — invoked at state-build time when
     redactSecrets is on. Called twice per release (redact=false and
     redact=true) so a deep-compare between the two trees yields the
     replacement map for the atlas-redact post-renderer. The non-redaction
     path (regular ArgoCD render) skips the second call entirely, so the
     extra walk only happens in the CI/redacted-render workflow.

Inputs (passed via `include "atlas.values.merged" $args`):
  .Values   The full helmfile-state .Values (atlas object lives here).
            .Values.atlas.{cwd, appTemplates, deploymentDefinitions,
            instance.{template, name}, deployment.{cluster,
            deploymentPath, ...}} are read by this template.
  .Release  Release context with at minimum .Name and .Namespace. At
            release-time the loader passes helmfile's real .Release; at
            state-build time stage-3 passes a synthetic dict with the
            release's name + namespace.
  .redact   Bool. When true, every SOPS-decrypted value tree is rewritten
            through atlas.redact.value BEFORE merge. Any later .yaml.gotmpl
            that pulls from a SOPS key sees the redacted derivative and
            renders accordingly — transitive taint without explicit
            tracking.

Returns: merged YAML as a string. Callers either emit directly (loader)
or `fromYaml` it (stage-3).

Resolution order (later overrides earlier — chart < template < instance < hierarchy):
  - hierarchy (FIRST PASS, baseline ctx for template/instance .gotmpls):
      global.values.{sops.yaml, yaml, yaml.gotmpl}
      group.values.{...} (only when cluster has a group)
      cluster.values.{...}
      deployment-level values.{sops.yaml, yaml, yaml.gotmpl}
  - template release.values: list (in declaration order)
  - instance apps[].values: list (in declaration order)
  - hierarchy (FINAL PASS — wins precedence over template + instance)
*/ -}}

{{- /*
============================================================
atlas.hierarchy.merged — Walk the global → group → cluster → deployment
hierarchy and emit the merged values as YAML.
============================================================

Used in two places:

  1. atlas.values.merged below — as the FIRST PASS baseline before
     template/instance values: lists are processed.

  2. helmfile.instance.yaml.gotmpl — as the tpl context for rendering
     the app template's helmfile.yaml.gotmpl. Without this, templates
     that reference `{{ .Values.<hierarchyKey> }}` in their body
     (jsonPatches inline maps, inline values maps, chart paths derived
     from cluster config, etc.) silently resolve those references to
     empty.

Inputs (passed via `include "atlas.hierarchy.merged" $args`):
  .Values   The full helmfile-state .Values (atlas object lives here).
            .Values.atlas.{cwd, deploymentDefinitions,
            deployment.{cluster, deploymentPath}} are read.
  .Release  Release context. Hierarchy .yaml.gotmpl files render with
            this value in scope. At release-time the loader passes
            helmfile's real .Release; at state-build time stage-3
            passes a synthetic placeholder dict (Name+Namespace
            unset). Hierarchy files that reference .Release.* should
            use values that survive the synthetic case (or accept
            different output between state-build and release-time
            renders, which is generally an anti-pattern).
  .redact   Bool. When true, every SOPS-decrypted value tree is
            rewritten through atlas.redact.value BEFORE merge.

Returns: merged hierarchy YAML as a string. Callers `fromYaml` it.
*/ -}}
{{- define "atlas.hierarchy.merged" -}}
{{- $cwd := .Values.atlas.cwd }}
{{- $hierarchyDir := printf "%s/%s" $cwd .Values.atlas.deploymentDefinitions }}
{{- $deploymentDir := dir .Values.atlas.deployment.deploymentPath }}
{{- $cluster := .Values.atlas.deployment.cluster }}
{{- $hierarchyFiles := list
    (printf "%s/global.values.sops.yaml"   $hierarchyDir)
    (printf "%s/global.values.yaml"        $hierarchyDir)
    (printf "%s/global.values.yaml.gotmpl" $hierarchyDir)
}}
{{- if contains "/" $cluster }}
  {{- $groupName := dir $cluster }}
  {{- $hierarchyFiles = concat $hierarchyFiles (list
      (printf "%s/%s/group.values.sops.yaml"   $hierarchyDir $groupName)
      (printf "%s/%s/group.values.yaml"        $hierarchyDir $groupName)
      (printf "%s/%s/group.values.yaml.gotmpl" $hierarchyDir $groupName)
  ) }}
{{- end }}
{{- $hierarchyFiles = concat $hierarchyFiles (list
    (printf "%s/%s/cluster.values.sops.yaml"   $hierarchyDir $cluster)
    (printf "%s/%s/cluster.values.yaml"        $hierarchyDir $cluster)
    (printf "%s/%s/cluster.values.yaml.gotmpl" $hierarchyDir $cluster)
    (printf "%s/values.sops.yaml"              $deploymentDir)
    (printf "%s/values.yaml"                   $deploymentDir)
    (printf "%s/values.yaml.gotmpl"            $deploymentDir)
) }}

{{- $hierarchy := dict }}
{{- range $f := $hierarchyFiles }}
  {{- if isFile $f }}
    {{- if hasSuffix ".sops.yaml" $f }}
      {{- $sopsRef := printf "ref+sops://%s?format=yaml" $f }}
      {{- $decrypted := fetchSecretValue $sopsRef | fromYaml }}
      {{- if $.redact }}
        {{- $decrypted = index (include "atlas.redact.value" $decrypted | fromJson) "v" }}
      {{- end }}
      {{- $hierarchy = mergeOverwrite $hierarchy $decrypted }}
    {{- else if hasSuffix ".yaml.gotmpl" $f }}
      {{- /* Tpl ctx: atlas object (from .Values) + the accumulating
           hierarchy. Authors of hierarchy gotmpl files routinely reference
           .Values.atlas.deployment.cluster, .Values.atlas.cwd, etc. to
           compose values from the deployment context, so atlas must be in
           scope. The accumulator (hierarchy seen so far) layers on top so
           later files can reference earlier hierarchy keys via the bare
           form (.foo) or the wrapped form (.Values.foo). */ -}}
      {{- $hCtx := mergeOverwrite (deepCopy $.Values) (deepCopy $hierarchy) }}
      {{- $_ := set $hCtx "Values" $hCtx }}
      {{- $_ := set $hCtx "Release" $.Release }}
      {{- $hierarchy = mergeOverwrite $hierarchy (tpl (readFile $f) $hCtx | fromYaml) }}
    {{- else }}
      {{- $hierarchy = mergeOverwrite $hierarchy (readFile $f | fromYaml) }}
    {{- end }}
  {{- end }}
{{- end }}
{{ $hierarchy | toYaml }}
{{- end -}}


{{- define "atlas.values.merged" -}}

{{- /* ====================== PATHS ====================== */ -}}
{{- $cwd := .Values.atlas.cwd }}
{{- $templateDir := printf "%s/%s/%s" $cwd .Values.atlas.appTemplates .Values.atlas.instance.template }}
{{- $templateFile := printf "%s/helmfile.yaml.gotmpl" $templateDir }}
{{- $deploymentPath := .Values.atlas.deployment.deploymentPath }}
{{- $deploymentDir := dir $deploymentPath }}

{{- /* ====================== HIERARCHY VALUES (FIRST PASS — BASELINE) ====================== */ -}}
{{- $hierarchy := include "atlas.hierarchy.merged" (dict
    "Values"  .Values
    "Release" .Release
    "redact"  $.redact
) | fromYaml }}

{{- /* ====================== TPL CONTEXT (atlas + hierarchy + Release) ====================== */ -}}
{{- /* Template authors expect `{{ .Values.<hierarchyKey> }}` in the
     template body to resolve. Build a context that has the merged
     hierarchy under .Values, alongside the atlas object and Release. */ -}}
{{- $ctx := mergeOverwrite (deepCopy .Values) (deepCopy $hierarchy) }}
{{- $_ := set $ctx "Values" $ctx }}
{{- $_ := set $ctx "Release" .Release }}

{{- /* ====================== TEMPLATE-LEVEL VALUES ====================== */ -}}
{{- /* Match the release this loader call serves. The template emits releases
     under their author-intended names (e.g. "vm"), but ATLAS may have
     munged them at state-build time so .Release.Name is "backend-vm" or
     "vm-cust-abc". Apply the same munge here so the comparison succeeds.
     Munge logic mirrors helmfile.instance.yaml.gotmpl: skip when
     instance.name == template, otherwise prefix or suffix per nameStyle. */ -}}
{{- $templateRendered := tpl (readFile $templateFile) $ctx | fromYaml }}
{{- $instanceName := .Values.atlas.instance.name }}
{{- $needsMunge := ne $instanceName .Values.atlas.instance.template }}
{{- $nameStyle := "prefix" }}
{{- if $needsMunge }}
  {{- $deploymentRenderedForStyle := tpl (readFile $deploymentPath) $ctx | fromYaml }}
  {{- range $app := $deploymentRenderedForStyle.apps }}
    {{- $iName := $app.template }}
    {{- if hasKey $app "name" }}{{- $iName = $app.name }}{{- end }}
    {{- if and (eq $app.template $.Values.atlas.instance.template) (eq $iName $instanceName) }}
      {{- if hasKey $app "nameStyle" }}{{- $nameStyle = $app.nameStyle }}{{- end }}
    {{- end }}
  {{- end }}
{{- end }}
{{- $thisRelease := dict }}
{{- range $rel := $templateRendered.releases }}
  {{- $expected := $rel.name }}
  {{- if $needsMunge }}
    {{- if eq $nameStyle "suffix" }}
      {{- $expected = printf "%s-%s" $rel.name $instanceName }}
    {{- else }}
      {{- $expected = printf "%s-%s" $instanceName $rel.name }}
    {{- end }}
  {{- end }}
  {{- if eq $expected $.Release.Name }}
    {{- $thisRelease = $rel }}
  {{- end }}
{{- end }}

{{- /* Seed the merged dict with the atlas object so .Values.atlas.* is
     visible to the chart and any release-time templating that reads it.
     User values can override via the same key, but in practice no user
     value collides with the atlas namespace. */ -}}
{{- $merged := dict "atlas" .Values.atlas }}
{{- range $entry := ($thisRelease | get "values" list) }}
  {{- if kindIs "string" $entry }}
    {{- $absPath := printf "%s/%s" $templateDir $entry }}
    {{- if isFile $absPath }}
      {{- if hasSuffix ".sops.yaml" $entry }}
        {{- $sopsRef := printf "ref+sops://%s?format=yaml" $absPath }}
        {{- $decrypted := fetchSecretValue $sopsRef | fromYaml }}
        {{- if $.redact }}
          {{- $decrypted = index (include "atlas.redact.value" $decrypted | fromJson) "v" }}
        {{- end }}
        {{- $merged = mergeOverwrite $merged $decrypted }}
      {{- else if hasSuffix ".yaml.gotmpl" $entry }}
        {{- $progCtx := mergeOverwrite (deepCopy $.Values) (deepCopy $hierarchy) }}
        {{- $progCtx = mergeOverwrite $progCtx (deepCopy $merged) }}
        {{- $_ := set $progCtx "Values" $progCtx }}
        {{- $_ := set $progCtx "Release" $.Release }}
        {{- $merged = mergeOverwrite $merged (tpl (readFile $absPath) $progCtx | fromYaml) }}
      {{- else }}
        {{- $merged = mergeOverwrite $merged (readFile $absPath | fromYaml) }}
      {{- end }}
    {{- end }}
  {{- else if kindIs "map" $entry }}
    {{- $merged = mergeOverwrite $merged $entry }}
  {{- end }}
{{- end }}

{{- /* ====================== INSTANCE-LEVEL VALUES ====================== */ -}}
{{- $deploymentRendered := tpl (readFile $deploymentPath) $ctx | fromYaml }}
{{- $thisInstance := dict }}
{{- range $app := $deploymentRendered.apps }}
  {{- $instanceName := $app.template }}
  {{- if hasKey $app "name" }}
    {{- $instanceName = $app.name }}
  {{- end }}
  {{- if and (eq $app.template $.Values.atlas.instance.template) (eq $instanceName $.Values.atlas.instance.name) }}
    {{- $thisInstance = $app }}
  {{- end }}
{{- end }}
{{- range $entry := ($thisInstance | get "values" list) }}
  {{- if kindIs "string" $entry }}
    {{- $absPath := printf "%s/%s" $deploymentDir $entry }}
    {{- if isFile $absPath }}
      {{- if hasSuffix ".sops.yaml" $entry }}
        {{- $sopsRef := printf "ref+sops://%s?format=yaml" $absPath }}
        {{- $decrypted := fetchSecretValue $sopsRef | fromYaml }}
        {{- if $.redact }}
          {{- $decrypted = index (include "atlas.redact.value" $decrypted | fromJson) "v" }}
        {{- end }}
        {{- $merged = mergeOverwrite $merged $decrypted }}
      {{- else if hasSuffix ".yaml.gotmpl" $entry }}
        {{- $progCtx := mergeOverwrite (deepCopy $.Values) (deepCopy $hierarchy) }}
        {{- $progCtx = mergeOverwrite $progCtx (deepCopy $merged) }}
        {{- $_ := set $progCtx "Values" $progCtx }}
        {{- $_ := set $progCtx "Release" $.Release }}
        {{- $merged = mergeOverwrite $merged (tpl (readFile $absPath) $progCtx | fromYaml) }}
      {{- else }}
        {{- $merged = mergeOverwrite $merged (readFile $absPath | fromYaml) }}
      {{- end }}
    {{- end }}
  {{- else if kindIs "map" $entry }}
    {{- $merged = mergeOverwrite $merged $entry }}
  {{- end }}
{{- end }}

{{- /* ====================== RELEASE.SECRETS (template-relative, merged AFTER values:) ====================== */ -}}
{{- /* Helmfile contract: env-level secrets are merged last regardless of
     declaration order. Replicate at the release level — every entry in
     release.secrets and apps[].secrets lands AFTER all values: entries.
     Paths resolve template-relative for release.secrets and deployment-
     relative for instance-level apps[].secrets. Decryption is via SOPS
     (helmfile fetchSecretValue), the same path used for .sops.yaml in
     values: lists, so non-SOPS files are not supported here. */ -}}
{{- range $entry := ($thisRelease | get "secrets" list) }}
  {{- if kindIs "string" $entry }}
    {{- $absPath := printf "%s/%s" $templateDir $entry }}
    {{- if isFile $absPath }}
      {{- $sopsRef := printf "ref+sops://%s?format=yaml" $absPath }}
      {{- $decrypted := fetchSecretValue $sopsRef | fromYaml }}
      {{- if $.redact }}
        {{- $decrypted = index (include "atlas.redact.value" $decrypted | fromJson) "v" }}
      {{- end }}
      {{- $merged = mergeOverwrite $merged $decrypted }}
    {{- end }}
  {{- end }}
{{- end }}

{{- /* ====================== INSTANCE.SECRETS (deployment-relative, merged LAST among secrets) ====================== */ -}}
{{- range $entry := ($thisInstance | get "secrets" list) }}
  {{- if kindIs "string" $entry }}
    {{- $absPath := printf "%s/%s" $deploymentDir $entry }}
    {{- if isFile $absPath }}
      {{- $sopsRef := printf "ref+sops://%s?format=yaml" $absPath }}
      {{- $decrypted := fetchSecretValue $sopsRef | fromYaml }}
      {{- if $.redact }}
        {{- $decrypted = index (include "atlas.redact.value" $decrypted | fromJson) "v" }}
      {{- end }}
      {{- $merged = mergeOverwrite $merged $decrypted }}
    {{- end }}
  {{- end }}
{{- end }}

{{- /* ====================== HIERARCHY OVERLAY (FINAL — PRECEDENCE) ====================== */ -}}
{{- $merged = mergeOverwrite $merged $hierarchy }}

{{- /* ====================== EMIT MERGED VALUES ====================== */ -}}
{{ $merged | toYaml }}

{{- end -}}
