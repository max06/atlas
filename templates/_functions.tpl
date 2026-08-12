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
