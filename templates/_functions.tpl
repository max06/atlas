{{- /*
# code:   language=helm

Release helper functions for helmfile template processing.
See also: _store.tpl (values store with taint tracking), _glob.tpl (glob matching).
*/ -}}


{{- /*
atlas.applyListOverride — Resolves relative file paths in a release list field
(strategicMergePatches, jsonPatches, transformers) and appends any
instance-level overrides from the deployment.yaml.

The release values: field does NOT flow through this helper. It is replaced
wholesale by helmfile.instance with the values-loader path, and the loader
runs the progressive merge (SOPS decryption, .yaml.gotmpl rendering, and
merging in declaration order) at release-evaluation time.

Context: dict with "release", "instance", "templateDir", "deploymentDir",
and "field". Template-level entries anchor template-relative,
instance-level entries anchor deployment-relative (mirroring how the
respective values: files resolve).
*/ -}}
{{- define "atlas.applyListOverride" -}}
  {{- $field       := .field }}
  {{- $templateDir := .templateDir }}

  {{- /* 1. Convert relative paths in the template's own definition */ -}}
  {{- if hasKey .release $field }}
    {{- $val := .release | get $field }}
    {{- if $val }}
      {{- $converted := include "convertPaths" (dict
        "targetPath" $templateDir
        "values"     (toJson $val)
        "field"      $field
      ) | fromJson }}
      {{- $_ := set .release $field $converted }}
    {{- end }}
  {{- end }}

  {{- /* 2. Append any instance-level overrides. String entries are file
       references authored in deployment.yaml — anchor them relative to
       the deployment dir (unanchored they would resolve against
       helmfile's cache dir, effectively undefined in remote
       consumption). Inline maps pass through untouched. */ -}}
  {{- if hasKey .instance $field }}
    {{- $toAdd := .instance | get $field list }}
    {{- if $toAdd }}
      {{- $convertedAdd := include "convertPaths" (dict
        "targetPath" .deploymentDir
        "values"     (toJson $toAdd)
        "field"      (printf "%s (instance-level)" $field)
      ) | fromJson }}
      {{- $current := .release | get $field list }}
      {{- $_ := set .release $field (concat $current $convertedAdd) }}
    {{- end }}
  {{- end }}
{{- end }}


{{- /*
convertPaths — Converts relative file paths to absolute paths relative to
the template directory. Non-string entries (inline maps) are passed through.
*/ -}}
{{- define "convertPaths" -}}
  {{- $newValues := list }}

  {{- range $entry := (.values | fromJson) }}
    {{- if kindIs "string" $entry }}
      {{- if isFile (printf "%s/%s" $.targetPath $entry ) }}
        {{- $newValues = append $newValues (printf "%s/%s" $.targetPath $entry) }}
      {{- else }}
        {{- /* A string entry is an explicit file reference by the template
             author — a missing file means the release would deploy without
             its patch/transformer, indistinguishable from success. Fail
             loudly instead of silently dropping the entry. */ -}}
        {{- fail (printf "%s: file not found: %s/%s" ($.field | default "convertPaths") $.targetPath $entry) }}
      {{- end }}
    {{- else }}
      {{- $newValues = append $newValues $entry  }}
    {{- end }}
  {{- end }}

  {{ $newValues | toJson }}
{{- end -}}

{{- /*
  atlas.deployment.definition — load and template one deployment.yaml.

  Input: dict with "Values" = the atlas sub-context (.Values.atlas.deployment.*
  set for the pair being resolved), optional "hierarchy" = the already merged
  state-build hierarchy (see below). Output: the templated deployment.yaml
  text; callers pipe it through fromYaml.

  Why templating: deployment.yaml authors may use `{{ .Values.<hierarchyKey> }}`
  in apps[].name, apps[].template or any other field. Without the hierarchy in
  scope those references resolve to empty strings, which silently malforms the
  per-instance fan-out (instance.name="" leaks through to helmfile.instance and
  breaks the auto-munge contract). The hierarchy (global → group → cluster →
  deployment) is walked with skipSecrets: this pass never decrypts SOPS files,
  so deployment.yaml structure must not derive from secrets — secrets resolve
  only in the stage-3 values-loader.

  .Release is a synthetic placeholder: helmfile's real .Release is only
  available later inside the values-loader. Hierarchy gotmpl files that
  reference .Release.* see empty values during this state-build pass; same
  caveat as helmfile.instance.yaml.gotmpl.

  Shared by helmfile.single.yaml.gotmpl (instance fan-out) and the discovery
  map mode of helmfile.all.yaml.gotmpl (deployment → templates edges), so both
  see the identical parsed definition.
*/ -}}
{{- define "atlas.deployment.definition" -}}
{{- if not (isFile .Values.atlas.deployment.deploymentPath) }}
  {{- fail (printf "Deployment file not found: %s" .Values.atlas.deployment.deploymentPath) }}
{{- end }}
{{- $synthRelease := dict "Name" "" "Namespace" "" }}
{{- /* Callers that already walked the hierarchy (discovery map mode needs it
     for the template render too) pass it as "hierarchy" to skip a second
     walk. Same walk either way: skipSecrets, synthetic .Release. */ -}}
{{- $hierarchy := dict }}
{{- if hasKey . "hierarchy" }}
  {{- $hierarchy = .hierarchy }}
{{- else }}
  {{- $hierarchy = include "atlas.hierarchy.merged" (dict
      "Values"      .Values
      "Release"     $synthRelease
      "redact"      false
      "skipSecrets" true
  ) | fromYaml }}
{{- end }}
{{- /* NOTE on .Values: avoid `set $ctx "Values" $ctx` (self-reference) — a
     circular map overflows the stack in Go's fmt.printValue when any error
     or debug path formats the context. Set .Release first, then snapshot
     .Values via deepCopy — same pattern as helmfile.instance.yaml.gotmpl
     and _values_loader.tpl. */ -}}
{{- $ctx := mergeOverwrite (deepCopy .Values) (deepCopy $hierarchy) }}
{{- $_ := set $ctx "Release" $synthRelease }}
{{- $_ := set $ctx "Values" (deepCopy $ctx) }}
{{- tpl (readFile .Values.atlas.deployment.deploymentPath) $ctx }}
{{- end -}}

{{- /*
  atlas.instance.template — render one app template's helmfile.yaml.gotmpl
  for one instance, at state-build time.

  Input: dict with
    "Values"     the atlas sub-context: .Values.atlas.deployment.* and
                 .Values.atlas.instance.{template, name} set for the instance
    "hierarchy"  the state-build hierarchy (atlas.hierarchy.merged with
                 skipSecrets and a synthetic .Release)
    "instance"   this instance's apps[] entry from the templated deployment.yaml
                 (dict when the deployment has no matching entry)
  Output: the rendered template text; callers pipe it through fromYaml.

  Context: atlas + hierarchy + synthetic .Release, plus the instance's inline
  map values. App templates may reference deployment-level values in their
  body (e.g. `{{ .Values.targetPort }}` inside an inline values map for a raw
  chart); file-based values: entries are skipped — the values-loader resolves
  those at release-time with .Release.* available. Instance inline values go
  in UNDER the hierarchy (re-overlay) so hierarchy keys win consistently,
  matching the final value precedence the loader establishes.

  Shared by helmfile.instance.yaml.gotmpl (the real release rewrite) and the
  discovery map mode of helmfile.all.yaml.gotmpl (which local charts a pair's
  releases use), so both see the identical release list. The intermediate
  .Values snapshot before the instance merge is deliberate: it reproduces the
  context helmfile.instance has always built, key for key.
*/ -}}
{{- define "atlas.instance.template" -}}
{{- $hierarchy := .hierarchy }}
{{- $templateFile := printf "%s/%s/%s/helmfile.yaml.gotmpl" .Values.atlas.cwd .Values.atlas.appTemplates .Values.atlas.instance.template }}
{{- $synthRelease := dict "Name" "" "Namespace" "" }}
{{- /* NOTE on .Values: avoid `set $ctx "Values" $ctx` (self-reference) — see
     atlas.deployment.definition. */ -}}
{{- $ctx := mergeOverwrite (deepCopy .Values) (deepCopy $hierarchy) }}
{{- $_ := set $ctx "Release" $synthRelease }}
{{- $_ := set $ctx "Values" (deepCopy $ctx) }}
{{- range $entry := (.instance | get "values" list) }}
  {{- if kindIs "map" $entry }}
    {{- $ctx = mergeOverwrite $ctx $entry }}
  {{- end }}
{{- end }}
{{- $ctx = mergeOverwrite $ctx (deepCopy $hierarchy) }}
{{- $_ := set $ctx "Values" (deepCopy $ctx) }}
{{- tpl (readFile $templateFile) $ctx }}
{{- end -}}

{{- /*
  atlas.chart.localDependencies — local subcharts a chart directory pulls in.

  Follows `dependencies[].repository: file://…` in Chart.yaml, recursively.
  Input: dict with "dir" (absolute chart directory) and "seen" (directories
  already collected — cycle guard). Output: JSON {"v": [absolute dirs]}
  (wrapped, because fromJson needs an object). Remote dependencies are not
  files in the repository and are skipped.
*/ -}}
{{- define "atlas.chart.localDependencies" -}}
{{- $found := list }}
{{- $chartFile := printf "%s/Chart.yaml" .dir }}
{{- if isFile $chartFile }}
  {{- range $dependency := (readFile $chartFile | fromYaml | get "dependencies" list) }}
    {{- $repository := $dependency | get "repository" "" }}
    {{- if hasPrefix "file://" $repository }}
      {{- $target := trimPrefix "file://" $repository }}
      {{- if not (isAbs $target) }}
        {{- $target = printf "%s/%s" $.dir $target }}
      {{- end }}
      {{- $target = clean $target }}
      {{- if and (isDir $target) (not (has $target $.seen)) (not (has $target $found)) }}
        {{- $found = append $found $target }}
        {{- $nested := include "atlas.chart.localDependencies" (dict "dir" $target "seen" (concat $.seen $found)) | fromJson }}
        {{- range $n := $nested.v }}
          {{- if not (has $n $found) }}{{- $found = append $found $n }}{{- end }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- end }}
{{- dict "v" $found | toJson }}
{{- end -}}
