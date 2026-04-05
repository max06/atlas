{{- /*
# code:   language=helm
*/ -}}

{{- /* ===================================================================
     atlas.redact — Structure-preserving value redaction.
     ===================================================================

     atlas.redact.value walks a scalar/map/list tree and produces a
     redacted mirror. The mirror preserves types (strings stay strings,
     numbers stay numbers, booleans stay booleans) and enough shape hints
     that a downstream reader recognizes "this was redacted" without
     leaking the underlying content.

     Redaction rules:
       Strings (length < 4): kept as-is (too short to carry entropy).
       Strings (length >= 4): split on non-alphanumeric characters; each
                              alphanumeric segment becomes the first
                              min(len, 8) characters of "REDACTED";
                              delimiters (dots, dashes, newlines, etc.)
                              preserved in place. A multi-line secret
                              naturally turns into a multi-line REDACTED
                              shape, which is what the diff-then-replace
                              pipeline expects.
       Numbers (< 5 digits):  kept as-is (low entropy).
       Numbers (>= 5 digits): each digit replaced with the next digit from
                              the cycle 1,2,3,4,5,6,7,8,9,0,1,2,... The
                              sign and decimal point are preserved.
       Booleans:              kept as-is (2 possible values).

     Everywhere a recursive template returns a value, it returns a single
     JSON document with one key "v", so callers can unwrap a typed value
     with `index (... | fromJson) "v"`. Go templates can only return
     strings from `include`, so this wrapper is the workaround. Sprig's
     `get` is positional (`get DICT KEY`) and doesn't compose with pipes,
     so we use `index` throughout.

     Usage:
       {{- $redacted := index (include "atlas.redact.value" $realValues | fromJson) "v" }}
*/ -}}

{{- /*
atlas.redact.value — Recursively redact every leaf in a value tree.
Context: the value to redact (map / slice / scalar).
Returns: a JSON document {"v": <redacted tree>} — unwrap via `index (... | fromJson) "v"`.
*/ -}}
{{- define "atlas.redact.value" -}}
  {{- if kindIs "map" . -}}
    {{- $out := dict -}}
    {{- range $k, $v := . -}}
      {{- $redacted := index (include "atlas.redact.value" $v | fromJson) "v" -}}
      {{- $_ := set $out $k $redacted -}}
    {{- end -}}
    {{- dict "v" $out | toJson -}}
  {{- else if kindIs "slice" . -}}
    {{- $out := list -}}
    {{- range $item := . -}}
      {{- $redacted := index (include "atlas.redact.value" $item | fromJson) "v" -}}
      {{- $out = append $out $redacted -}}
    {{- end -}}
    {{- dict "v" $out | toJson -}}
  {{- else if kindIs "bool" . -}}
    {{- dict "v" . | toJson -}}
  {{- else if or (kindIs "int" .) (kindIs "int64" .) (kindIs "float64" .) -}}
    {{- /* Re-parse through fromJson so the caller gets back the same numeric
         type it handed us (fromJson produces float64 for all numbers, which
         matches what helm ultimately uses in templates). */ -}}
    {{- $redactedStr := include "atlas.redact.number" . -}}
    {{- printf "{\"v\": %s}" $redactedStr -}}
  {{- else -}}
    {{- /* Treat everything else as a string. */ -}}
    {{- $redactedStr := include "atlas.redact.string" (toString .) -}}
    {{- dict "v" $redactedStr | toJson -}}
  {{- end -}}
{{- end -}}

{{- /*
atlas.redact.string — Redact a single string value.
Context: the string to redact.
Returns: the redacted string (raw, no wrapper — call sites handle wrapping).
*/ -}}
{{- define "atlas.redact.string" -}}
  {{- $input := . -}}
  {{- if lt (len $input) 4 -}}
    {{- $input -}}
  {{- else -}}
    {{- /* Walk each character; accumulate runs of alphanumerics as
         "segments" and flush them as REDACTED[:min(len, 8)] whenever a
         non-alphanumeric character appears. Delimiters are passed through
         verbatim, which preserves dots, dashes, spaces, and newlines. */ -}}
    {{- $result := "" -}}
    {{- $segLen := 0 -}}
    {{- $template := "REDACTED" -}}
    {{- range $i := until (len $input) -}}
      {{- $c := substr $i (add $i 1 | int) $input -}}
      {{- if regexMatch "^[a-zA-Z0-9]$" $c -}}
        {{- $segLen = add $segLen 1 | int -}}
      {{- else -}}
        {{- if gt $segLen 0 -}}
          {{- $take := min $segLen 8 | int -}}
          {{- $result = printf "%s%s" $result (substr 0 $take $template) -}}
          {{- $segLen = 0 -}}
        {{- end -}}
        {{- $result = printf "%s%s" $result $c -}}
      {{- end -}}
    {{- end -}}
    {{- if gt $segLen 0 -}}
      {{- $take := min $segLen 8 | int -}}
      {{- $result = printf "%s%s" $result (substr 0 $take $template) -}}
    {{- end -}}
    {{- $result -}}
  {{- end -}}
{{- end -}}

{{- /*
atlas.diff.values — Walk two parallel value trees (real + redacted) and
produce a flat replacement map {real_leaf: redacted_leaf} covering every
leaf where the two trees differ.

Context: dict with "real" and "redacted" keys. The trees are expected to be
structurally identical (same keys, same list lengths); atlas.redact.value
preserves structure, so this holds after twin-load.

Returns: JSON document {"v": {real_str: redacted_str, ...}} — the caller
unwraps with `index (... | fromJson) "v"`, then serializes the inner dict
once (as JSON or YAML) and base64-encodes it for transport to the plugin.
That way we encode exactly once at the boundary, instead of per value.
*/ -}}
{{- define "atlas.diff.values" -}}
  {{- $real := .real -}}
  {{- $redacted := .redacted -}}
  {{- $out := dict -}}
  {{- if kindIs "map" $real -}}
    {{- range $k, $v := $real -}}
      {{- $r := "" -}}
      {{- if and (kindIs "map" $redacted) (hasKey $redacted $k) -}}
        {{- $r = index $redacted $k -}}
      {{- end -}}
      {{- $sub := index (include "atlas.diff.values" (dict "real" $v "redacted" $r) | fromJson) "v" -}}
      {{- range $rk, $rv := $sub -}}
        {{- $_ := set $out $rk $rv -}}
      {{- end -}}
    {{- end -}}
  {{- else if kindIs "slice" $real -}}
    {{- range $i, $v := $real -}}
      {{- $r := "" -}}
      {{- if and (kindIs "slice" $redacted) (lt $i (len $redacted)) -}}
        {{- $r = index $redacted $i -}}
      {{- end -}}
      {{- $sub := index (include "atlas.diff.values" (dict "real" $v "redacted" $r) | fromJson) "v" -}}
      {{- range $rk, $rv := $sub -}}
        {{- $_ := set $out $rk $rv -}}
      {{- end -}}
    {{- end -}}
  {{- else -}}
    {{- /* Scalar comparison — any tree node that's not a map or slice.
         Bools, strings, numbers all get stringified for equality since
         types can shift through YAML re-parsing (int → float64 is the
         most common surprise) but the string form stays stable. Same key
         re-assignment is fine: atlas.redact.value is deterministic, so
         identical real values always map to identical redacted values. */ -}}
    {{- $realStr := toString $real -}}
    {{- $redactedStr := toString $redacted -}}
    {{- if ne $realStr $redactedStr -}}
      {{- $_ := set $out $realStr $redactedStr -}}
    {{- end -}}
  {{- end -}}
  {{- dict "v" $out | toJson -}}
{{- end -}}

{{- /*
atlas.redact.number — Redact a numeric value.
Context: the number (any numeric kind).
Returns: the redacted number as a string that's valid JSON/YAML (e.g.
"42", "-123456", "1.23456789"). Callers wrap into {"v": ...} themselves.
*/ -}}
{{- define "atlas.redact.number" -}}
  {{- $str := printf "%v" . -}}
  {{- $digits := regexReplaceAll "[^0-9]" $str "" -}}
  {{- if lt (len $digits) 5 -}}
    {{- $str -}}
  {{- else -}}
    {{- /* Replace each digit with the next from the cycle 1,2,...,9,0.
         Non-digit characters (the minus sign, the decimal point) stay
         where they are, preserving the original shape. */ -}}
    {{- $result := "" -}}
    {{- $cycle := "1234567890" -}}
    {{- $idx := 0 -}}
    {{- range $i := until (len $str) -}}
      {{- $c := substr $i (add $i 1 | int) $str -}}
      {{- if regexMatch "^[0-9]$" $c -}}
        {{- $pos := mod $idx 10 | int -}}
        {{- $result = printf "%s%s" $result (substr $pos (add $pos 1 | int) $cycle) -}}
        {{- $idx = add $idx 1 | int -}}
      {{- else -}}
        {{- $result = printf "%s%s" $result $c -}}
      {{- end -}}
    {{- end -}}
    {{- $result -}}
  {{- end -}}
{{- end -}}
