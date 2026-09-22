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
  set for the pair being resolved). Output: the templated deployment.yaml text;
  callers pipe it through fromYaml.

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
{{- $hierarchy := include "atlas.hierarchy.merged" (dict
    "Values"      .Values
    "Release"     $synthRelease
    "redact"      false
    "skipSecrets" true
) | fromYaml }}
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
