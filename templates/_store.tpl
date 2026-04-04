{{- /*
# code:   language=helm
*/ -}}

{{- /* ===================================================================
     atlas.store — A values container with taint tracking for secrets.
     ===================================================================

     The store is a plain dict with two keys:
       _values  — the merged values map (what templates and Helm see)
       _taints  — a parallel map tracking which top-level keys are tainted

     All mutating functions receive the store as context (.) and modify it
     in place via `set`. They return YAML comment lines with a visual trace
     of the loading process, visible via `helmfile build --debug`.

     Taint types:
       "direct"  — key came from a SOPS-encrypted file
       "pointer" — key's value was derived from a tainted key via gotmpl

     Usage in helmfile.single.yaml.gotmpl:
       {{- $store := include "atlas.store.init" .Values.atlas | fromYaml }}
       {{- include "atlas.store.mergeSops" (dict "store" $store "source" $sopsFile) }}
       {{- include "atlas.store.mergeYaml" (dict "store" $store "source" $yamlFile) }}
       {{- include "atlas.store.mergeTpl"  (dict "store" $store "source" $gotmplFile) }}
       {{- $loadedValues := include "atlas.store.values" $store | fromYaml }}
       {{- $outputValues := include "atlas.store.redactedValues" $store | fromYaml }}
*/ -}}

{{- /*
atlas.store.init — Creates a new store with initial atlas values seeded.
Expects the atlas config object as context.
Returns a YAML-serialized store dict (use | fromYaml to get the dict).
*/ -}}
{{- define "atlas.store.init" -}}
  {{- dict "_values" (dict "atlas" .) "_taints" (dict) | toYaml -}}
{{- end -}}

{{- /*
atlas.store.formatValue — Formats a scalar value for debug trace output.
Returns a display string like: "hello" or 42 (num) or true (bool)
*/ -}}
{{- define "atlas.store.formatValue" -}}
  {{- if kindIs "bool" . -}}
    {{- printf "%v (bool)" . -}}
  {{- else if or (kindIs "float64" .) (kindIs "int" .) (kindIs "int64" .) -}}
    {{- printf "%v (num)" . -}}
  {{- else -}}
    {{- $str := toString . -}}
    {{- if gt (len $str) 40 -}}
      {{- printf "\"%s...\" (%d chars)" (substr 0 37 $str) (len $str) -}}
    {{- else -}}
      {{- printf "\"%s\"" $str -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- /*
atlas.store.traceEntry — Renders a debug trace block for a single key/value pair.
Recursively expands maps and lists with indentation.
Context: dict with "key", "val", "old" (previous value or nil), "indent" (prefix
string like "#   " or "#     "), and "taint" (taint label string or "").
*/ -}}
{{- define "atlas.store.traceEntry" -}}
  {{- $key    := .key -}}
  {{- $val    := .val -}}
  {{- $old    := .old -}}
  {{- $indent := .indent -}}
  {{- $taint  := .taint -}}
  {{- $hasOld := .hasOld -}}
  {{- if kindIs "map" $val -}}
    {{- if $hasOld }}
{{ $indent }}~ {{ $key }}:{{ $taint }}
    {{- else }}
{{ $indent }}+ {{ $key }}:{{ $taint }}
    {{- end -}}
    {{- $childIndent := printf "%s    " $indent -}}
    {{- range $k, $v := $val -}}
      {{- $childOld := dict -}}
      {{- $childHasOld := false -}}
      {{- if and $hasOld (kindIs "map" $old) (hasKey $old $k) -}}
        {{- $childOld = index $old $k -}}
        {{- $childHasOld = true -}}
      {{- end -}}
      {{- include "atlas.store.traceEntry" (dict "key" $k "val" $v "old" $childOld "hasOld" $childHasOld "indent" $childIndent "taint" "") -}}
    {{- end -}}
  {{- else if kindIs "slice" $val -}}
    {{- if $hasOld }}
{{ $indent }}~ {{ $key }}: [{{ len $val }} items]{{ $taint }}
    {{- else }}
{{ $indent }}+ {{ $key }}: [{{ len $val }} items]{{ $taint }}
    {{- end -}}
    {{- $childIndent := printf "%s    " $indent -}}
    {{- range $i, $item := $val }}
{{ $childIndent }}- {{ include "atlas.store.formatValue" $item }}
    {{- end -}}
  {{- else -}}
    {{- if $hasOld }}
{{ $indent }}~ {{ $key }} = {{ include "atlas.store.formatValue" $val }} ← was {{ include "atlas.store.formatValue" $old }}{{ $taint }}
    {{- else }}
{{ $indent }}+ {{ $key }} = {{ include "atlas.store.formatValue" $val }}{{ $taint }}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- /*
atlas.store.mergeSops — Decrypts a SOPS file and merges its values into the store.
All top-level keys from the SOPS file are marked as directly tainted.
Context: dict with "store" (the store dict) and "source" (file path).
The ?format=yaml query parameter is required because vals can lose the file
extension during URI parsing, causing SOPS to try JSON (helmfile/helmfile#2001).
*/ -}}
{{- define "atlas.store.mergeSops" -}}
  {{- $store  := .store -}}
  {{- $source := .source -}}
  {{- $values := index $store "_values" -}}
  {{- $taints := index $store "_taints" -}}
  {{- $sopsRef       := printf "ref+sops://%s?format=yaml" $source -}}
  {{- $decryptedYaml := fetchSecretValue $sopsRef -}}
  {{- $decryptedData := $decryptedYaml | fromYaml }}
# STORE: Loading {{ base $source }} (sops)
  {{- range $key, $val := $decryptedData -}}
    {{- $old := dict -}}
    {{- $hasOld := false -}}
    {{- if hasKey $values $key -}}
      {{- $old = index $values $key -}}
      {{- $hasOld = true -}}
    {{- end -}}
    {{- include "atlas.store.traceEntry" (dict "key" $key "val" $val "old" $old "hasOld" $hasOld "indent" "#  " "taint" " [taint: direct]") -}}
  {{- end }}
  {{- range $key, $val := $decryptedData -}}
    {{- $_ := set $taints $key "direct" -}}
  {{- end -}}
  {{- $merged := mergeOverwrite $values $decryptedData -}}
  {{- $_ := set $store "_values" $merged -}}
{{- end -}}

{{- /*
atlas.store.mergeYaml — Renders a YAML file as a Go template and merges the result.
Plain .yaml files pass through tpl unchanged (no expressions to evaluate).
No taint tracking — values from plain YAML are considered clean.
Context: dict with "store" (the store dict) and "source" (file path).
*/ -}}
{{- define "atlas.store.mergeYaml" -}}
  {{- $store  := .store -}}
  {{- $source := .source -}}
  {{- $values := index $store "_values" -}}
  {{- $rendered := tpl (readFile $source) $values -}}
  {{- $parsed   := fromYaml $rendered }}
# STORE: Loading {{ base $source }}
  {{- range $key, $val := $parsed -}}
    {{- $old := dict -}}
    {{- $hasOld := false -}}
    {{- if hasKey $values $key -}}
      {{- $old = index $values $key -}}
      {{- $hasOld = true -}}
    {{- end -}}
    {{- include "atlas.store.traceEntry" (dict "key" $key "val" $val "old" $old "hasOld" $hasOld "indent" "#  " "taint" "") -}}
  {{- end }}
  {{- $merged := mergeOverwrite $values $parsed -}}
  {{- $_ := set $store "_values" $merged -}}
{{- end -}}

{{- /*
atlas.store.mergeTpl — Renders a .gotmpl file and merges the result, with taint
propagation. Scans the raw template source for references to tainted keys — if a
line defining output key "foo" contains a reference to a tainted key, then "foo"
inherits the taint as a pointer.
Context: dict with "store" (the store dict) and "source" (file path).
*/ -}}
{{- define "atlas.store.mergeTpl" -}}
  {{- $store  := .store -}}
  {{- $source := .source -}}
  {{- $values := index $store "_values" -}}
  {{- $taints := index $store "_taints" -}}
  {{- $rawContent := readFile $source -}}
  {{- $rendered   := tpl $rawContent $values -}}
  {{- $parsed     := fromYaml $rendered }}
# STORE: Loading {{ base $source }} (gotmpl)
  {{- /* Scan for pointer taints: for each output key, check if any tainted key
       is referenced on the same line in the raw template source. */ -}}
  {{- range $key, $val := $parsed -}}
    {{- $pointerSource := "" -}}
    {{- range $taintedKey, $_ := $taints -}}
      {{- $refPattern := printf ".%s" $taintedKey -}}
      {{- if contains $refPattern $rawContent -}}
        {{- $keyPattern := printf "%s:" $key -}}
        {{- range $line := splitList "\n" $rawContent -}}
          {{- if and (contains $keyPattern $line) (contains $refPattern $line) -}}
            {{- $_ := set $taints $key "pointer" -}}
            {{- $pointerSource = $taintedKey -}}
          {{- end -}}
        {{- end -}}
      {{- end -}}
    {{- end -}}
    {{- $taintLabel := "" -}}
    {{- if $pointerSource -}}
      {{- $taintLabel = printf " [taint: pointer → %s]" $pointerSource -}}
    {{- end -}}
    {{- $old := dict -}}
    {{- $hasOld := false -}}
    {{- if hasKey $values $key -}}
      {{- $old = index $values $key -}}
      {{- $hasOld = true -}}
    {{- end -}}
    {{- include "atlas.store.traceEntry" (dict "key" $key "val" $val "old" $old "hasOld" $hasOld "indent" "#  " "taint" $taintLabel) -}}
  {{- end -}}
  {{- $merged := mergeOverwrite $values $parsed -}}
  {{- $_ := set $store "_values" $merged -}}
{{- end -}}

{{- /*
atlas.store.values — Returns the plain values map from the store.
Use this for tpl rendering contexts where real values are needed (deployment.yaml,
app templates, gotmpl files). The returned map contains unredacted values so that
template expressions resolve correctly.
Context: the store dict.
*/ -}}
{{- define "atlas.store.values" -}}
  {{- index . "_values" | toYaml -}}
{{- end -}}

{{- /*
atlas.store.taintedValues — Returns all tainted values as a ||-delimited string.
Each tainted key is resolved to its current value(s) in the store. Maps and lists
are recursively flattened to their leaf values. Used as the postRendererArgs for
the atlas-redact Helm plugin.
Context: the store dict.
*/ -}}
{{- define "atlas.store.taintedValues" -}}
  {{- $values := index . "_values" -}}
  {{- $taints := index . "_taints" -}}
  {{- $result := list -}}
  {{- range $key, $_ := $taints -}}
    {{- if hasKey $values $key -}}
      {{- $val := index $values $key -}}
      {{- $leaves := include "atlas.store.collectLeaves" $val | fromJson -}}
      {{- range $leaf := $leaves -}}
        {{- $result = append $result $leaf -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
  {{- $result | join "||" -}}
{{- end -}}

{{- /*
atlas.store.collectLeaves — Recursively collects all leaf (scalar) values from
a value that may be a scalar, map, or list. Returns a JSON array of strings.
*/ -}}
{{- define "atlas.store.collectLeaves" -}}
  {{- $results := list -}}
  {{- if kindIs "map" . -}}
    {{- range $_, $v := . -}}
      {{- $sub := include "atlas.store.collectLeaves" $v | fromJson -}}
      {{- range $sub -}}
        {{- $results = append $results . -}}
      {{- end -}}
    {{- end -}}
  {{- else if kindIs "slice" . -}}
    {{- range $item := . -}}
      {{- $sub := include "atlas.store.collectLeaves" $item | fromJson -}}
      {{- range $sub -}}
        {{- $results = append $results . -}}
      {{- end -}}
    {{- end -}}
  {{- else -}}
    {{- $results = append $results (toString .) -}}
  {{- end -}}
  {{- $results | toJson -}}
{{- end -}}

{{- /*
atlas.store.redactedValues — Returns a deep copy of the values map with tainted
keys redacted by type. Use this for output that may be visible (PR comments,
CI logs, rendered manifests). The original store is not modified.
Redaction rules:
  - strings: always → "REDACTED"
  - numbers >= 5 digits: → 0
  - numbers < 5 digits: kept
  - booleans: kept
Context: the store dict.
*/ -}}
{{- define "atlas.store.redactedValues" -}}
  {{- $values := deepCopy (index . "_values") -}}
  {{- $taints := index . "_taints" -}}
  {{- range $key, $_ := $taints -}}
    {{- if hasKey $values $key -}}
      {{- $val := index $values $key -}}
      {{- $redacted := include "atlas.redactValue" $val | fromYaml -}}
      {{- $_ := set $values $key (index $redacted "v") -}}
    {{- end -}}
  {{- end -}}
  {{- $values | toYaml -}}
{{- end -}}

{{- /*
atlas.redactValue — Type-aware redaction of a single value.
Recursively handles maps and lists. Returns YAML with the value wrapped
under key "v" so that scalars survive fromYaml (e.g., {v: REDACTED}).
Redaction rules:
  - strings: always → "REDACTED"
  - numbers >= 5 digits: → 0
  - numbers < 5 digits: kept
  - booleans: kept
*/ -}}
{{- define "atlas.redactValue" -}}
  {{- if kindIs "map" . -}}
    {{- $result := dict -}}
    {{- range $k, $v := . -}}
      {{- $redacted := include "atlas.redactValue" $v | fromYaml -}}
      {{- $_ := set $result $k (index $redacted "v") -}}
    {{- end -}}
    {{- dict "v" $result | toYaml -}}
  {{- else if kindIs "slice" . -}}
    {{- $result := list -}}
    {{- range $item := . -}}
      {{- $redacted := include "atlas.redactValue" $item | fromYaml -}}
      {{- $result = append $result (index $redacted "v") -}}
    {{- end -}}
    {{- dict "v" $result | toYaml -}}
  {{- else if kindIs "bool" . -}}
    {{- dict "v" . | toYaml -}}
  {{- else if or (kindIs "float64" .) (kindIs "int" .) (kindIs "int64" .) -}}
    {{- $str := toString . -}}
    {{- $digits := regexReplaceAll "[^0-9]" $str "" -}}
    {{- if ge (len $digits) 5 -}}
      {{- dict "v" 0 | toYaml -}}
    {{- else -}}
      {{- dict "v" . | toYaml -}}
    {{- end -}}
  {{- else -}}
    {{- dict "v" "REDACTED" | toYaml -}}
  {{- end -}}
{{- end -}}
