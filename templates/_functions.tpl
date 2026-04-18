{{- /*
# code:   language=helm

Release helper functions for helmfile template processing.
See also: _store.tpl (values store with taint tracking), _glob.tpl (glob matching).
*/ -}}


{{- /*
atlas.applyListOverride — Resolves relative file paths in a release list field
(strategicMergePatches, jsonPatches, transformers) and appends any
instance-level overrides from the deployment.yaml.

The release values: field does NOT flow through this helper. It is handled
inline in helmfile.single.yaml.gotmpl via a progressive twin-load merge so
that later list entries can reference keys defined by earlier ones (SOPS
decryption, .yaml.gotmpl rendering, and merging all happen in declaration
order within the same loop).

Context: dict with "release", "instance", "templateDir", and "field".
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
      ) | fromJson }}
      {{- $_ := set .release $field $converted }}
    {{- end }}
  {{- end }}

  {{- /* 2. Append any instance-level overrides */ -}}
  {{- if hasKey .instance $field }}
    {{- $toAdd := .instance | get $field list }}
    {{- if $toAdd }}
      {{- $current := .release | get $field list }}
      {{- $_ := set .release $field (concat $current $toAdd) }}
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
      {{- end }}
    {{- else }}
      {{- $newValues = append $newValues $entry  }}
    {{- end }}
  {{- end }}

  {{ $newValues | toJson }}
{{- end -}}
