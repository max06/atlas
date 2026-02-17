{{- /*
# code:   language=helm
*/ -}}


{{- define "debugEnabled" -}}
  {{- .Values | get "atlas.debug" false -}}
{{- end -}}


{{- define "atlas.applyListOverride" -}}
  {{- /* Only scalar locals – never re-assign complex maps from . */ -}}
  {{- $field       := .field }}
  {{- $templateDir := .templateDir }}

  {{- /* 1. Convert relative paths in the *template's own* definition */ -}}
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


{{- define "convertPaths" -}}
  {{- $newValues := list }}

  {{- /* # Iterate over values, check for file, adapt path */ -}}
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

{{- define "glob" -}}
  {{- $pattern := . -}}
  {{- $results := list -}}

  {{- /* Check if pattern contains ** for recursive matching */ -}}
  {{- $hasRecursive := contains "**" $pattern -}}

  {{- if $hasRecursive -}}
    {{- /* Handle recursive globbing */ -}}
    {{- $parts := splitList "**" $pattern -}}
    {{- $prefix := index $parts 0 | trimSuffix "/" -}}
    {{- $suffix := "" -}}
    {{- if gt (len $parts) 1 -}}
      {{- $suffix = index $parts 1 | trimPrefix "/" -}}
    {{- end -}}

    {{- /* Start recursive search from prefix directory */ -}}
    {{- $startDir := $prefix -}}
    {{- if eq $startDir "" -}}
      {{- $startDir = "." -}}
    {{- end -}}

    {{- $results = include "globRecursive" (dict "dir" $startDir "pattern" $suffix "prefix" $prefix) | fromJson -}}
  {{- else -}}
    {{- /* Check if pattern has wildcards in directory parts */ -}}
    {{- $parts := splitList "/" $pattern -}}
    {{- $hasWildcardInPath := false -}}
    {{- range $idx, $part := $parts -}}
      {{- if and (contains "*" $part) (lt $idx (sub (len $parts) 1)) -}}
        {{- $hasWildcardInPath = true -}}
      {{- end -}}
    {{- end -}}

    {{- if $hasWildcardInPath -}}
      {{- /* Use iterative matching for wildcards in path */ -}}
      {{- $results = include "globIterative" (dict "parts" $parts "index" 0 "currentPath" "") | fromJson -}}
    {{- else -}}
      {{- /* Simple glob - wildcard only in filename */ -}}
      {{- $dir := dir $pattern -}}
      {{- $base := base $pattern -}}

      {{- if eq $dir "." -}}
        {{- $dir = "" -}}
      {{- end -}}

      {{- if or (eq $dir "") (isDir $dir) -}}
        {{- $entries := readDirEntries $dir -}}

        {{- range $entries -}}
          {{- $entryName := .Name -}}
          {{- $fullPath := $entryName -}}
          {{- if ne $dir "" -}}
            {{- $fullPath = printf "%s/%s" $dir $entryName -}}
          {{- end -}}

          {{- /* Wildcard matching */ -}}
          {{- if contains "*" $base -}}
            {{- $regex := regexReplaceAll "\\*" $base ".*" -}}
            {{- $regex = regexReplaceAll "\\?" $regex "." -}}
            {{- if regexMatch (printf "^%s$" $regex) $entryName -}}
              {{- $results = append $results $fullPath -}}
            {{- end -}}
          {{- else if eq $base $entryName -}}
            {{- $results = append $results $fullPath -}}
          {{- end -}}
        {{- end -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}

  {{- $results | toJson -}}
{{- end -}}

{{- define "globIterative" -}}
  {{- $parts := .parts -}}
  {{- $index := .index -}}
  {{- $currentPath := .currentPath -}}
  {{- $results := list -}}

  {{- if lt $index (len $parts) -}}
    {{- $part := index $parts $index -}}
    {{- $isLast := eq $index (sub (len $parts) 1) -}}

    {{- /* Handle empty parts (from leading slash in absolute paths) */ -}}
    {{- if eq $part "" -}}
      {{- /* Skip empty part and continue to next, marking as absolute path */ -}}
      {{- $newCurrentPath := $currentPath -}}
      {{- if and (eq $index 0) (eq $currentPath "") -}}
        {{- /* First part is empty = absolute path starting with / */ -}}
        {{- $newCurrentPath = "/" -}}
      {{- end -}}
      {{- $subResults := include "globIterative" (dict "parts" $parts "index" (add1 $index) "currentPath" $newCurrentPath) | fromJson -}}
      {{- range $subResults -}}
        {{- $results = append $results . -}}
      {{- end -}}
    {{- else -}}
      {{- /* Determine the directory to search in */ -}}
      {{- $searchDir := $currentPath -}}
      {{- if eq $searchDir "" -}}
        {{- $searchDir = "." -}}
      {{- end -}}

      {{- if isDir $searchDir -}}
        {{- $entries := readDirEntries $searchDir -}}

        {{- /* Match entries against current part pattern */ -}}
        {{- if contains "*" $part -}}
          {{- /* Wildcard matching */ -}}
          {{- $regex := regexReplaceAll "\\*" $part ".*" -}}
          {{- $regex = regexReplaceAll "\\?" $regex "." -}}

          {{- range $entries -}}
            {{- $entryName := .Name -}}
            {{- if regexMatch (printf "^%s$" $regex) $entryName -}}
              {{- $newPath := $entryName -}}
              {{- if ne $currentPath "" -}}
                {{- if eq $currentPath "/" -}}
                  {{- /* Root path - don't add extra slash */ -}}
                  {{- $newPath = printf "/%s" $entryName -}}
                {{- else if hasPrefix $currentPath "/" -}}
                  {{- /* Absolute path - just append */ -}}
                  {{- $newPath = printf "%s/%s" $currentPath $entryName -}}
                {{- else -}}
                  {{- /* Relative path */ -}}
                  {{- $newPath = printf "%s/%s" $currentPath $entryName -}}
                {{- end -}}
              {{- end -}}

              {{- if $isLast -}}
                {{- /* This is the last part, add matching entries */ -}}
                {{- $results = append $results $newPath -}}
              {{- else -}}
                {{- /* Recurse to next part */ -}}
                {{- $subResults := include "globIterative" (dict "parts" $parts "index" (add1 $index) "currentPath" $newPath) | fromJson -}}
                {{- range $subResults -}}
                  {{- $results = append $results . -}}
                {{- end -}}
              {{- end -}}
            {{- end -}}
          {{- end -}}
        {{- else -}}
          {{- /* Exact match */ -}}
          {{- $newPath := $part -}}
          {{- if ne $currentPath "" -}}
            {{- if eq $currentPath "/" -}}
              {{- /* Root path - don't add extra slash */ -}}
              {{- $newPath = printf "/%s" $part -}}
            {{- else if hasPrefix $currentPath "/" -}}
              {{- /* Absolute path - just append */ -}}
              {{- $newPath = printf "%s/%s" $currentPath $part -}}
            {{- else -}}
              {{- /* Relative path */ -}}
              {{- $newPath = printf "%s/%s" $currentPath $part -}}
            {{- end -}}
          {{- end -}}

          {{- if $isLast -}}
            {{- if or (isFile $newPath) (isDir $newPath) -}}
              {{- $results = append $results $newPath -}}
            {{- end -}}
          {{- else -}}
            {{- $subResults := include "globIterative" (dict "parts" $parts "index" (add1 $index) "currentPath" $newPath) | fromJson -}}
            {{- range $subResults -}}
              {{- $results = append $results . -}}
            {{- end -}}
          {{- end -}}
        {{- end -}}
      {{- end -}}
    {{- end -}}
  {{- else -}}
    {{- /* Base case: we've processed all parts */ -}}
    {{- if or (isFile $currentPath) (isDir $currentPath) -}}
      {{- $results = append $results $currentPath -}}
    {{- end -}}
  {{- end -}}

  {{- $results | toJson -}}
{{- end -}}

{{- define "globRecursive" -}}
  {{- $dir := .dir -}}
  {{- $pattern := .pattern -}}
  {{- $prefix := .prefix -}}
  {{- $results := list -}}

  {{- /* Read current directory */ -}}
  {{- if isDir $dir -}}
    {{- $entries := readDirEntries $dir -}}

    {{- range $entries -}}
      {{- $entry := . -}}
      {{- $entryName := $entry.Name -}}

      {{- /* Build full path preserving absolute/relative nature */ -}}
      {{- $fullPath := "" -}}
      {{- if hasPrefix $dir "/" -}}
        {{- /* Absolute path */ -}}
        {{- $fullPath = printf "%s/%s" (trimSuffix "/" $dir) $entryName -}}
      {{- else if eq $dir "." -}}
        {{- /* Current directory - just use entry name */ -}}
        {{- $fullPath = $entryName -}}
      {{- else -}}
        {{- /* Relative path */ -}}
        {{- $fullPath = printf "%s/%s" (trimSuffix "/" $dir) $entryName -}}
      {{- end -}}

      {{- /* If there's a pattern after **, check if this entry matches */ -}}
      {{- if ne $pattern "" -}}
        {{- /* Check if pattern has wildcards in path - use globIterative */ -}}
        {{- $patternParts := splitList "/" $pattern -}}
        {{- $hasWildcardInPath := false -}}
        {{- range $idx, $part := $patternParts -}}
          {{- if and (contains "*" $part) (lt $idx (sub (len $patternParts) 1)) -}}
            {{- $hasWildcardInPath = true -}}
          {{- end -}}
        {{- end -}}

        {{- if $hasWildcardInPath -}}
          {{- /* Use iterative matching from this point */ -}}
          {{- $matched := include "globIterative" (dict "parts" $patternParts "index" 0 "currentPath" $fullPath) | fromJson -}}
          {{- range $matched -}}
            {{- $results = append $results . -}}
          {{- end -}}
        {{- else -}}
          {{- /* Simple pattern matching */ -}}
          {{- $firstPart := index $patternParts 0 -}}

          {{- /* Check if entry matches the pattern */ -}}
          {{- $matches := false -}}
          {{- if contains "*" $firstPart -}}
            {{- $regex := regexReplaceAll "\\*" $firstPart ".*" -}}
            {{- $regex = regexReplaceAll "\\?" $regex "." -}}
            {{- if regexMatch (printf "^%s$" $regex) $entryName -}}
              {{- $matches = true -}}
            {{- end -}}
          {{- else if eq $firstPart $entryName -}}
            {{- $matches = true -}}
          {{- end -}}

          {{- /* If it matches and it's the last part of pattern, add to results */ -}}
          {{- if and $matches (eq (len $patternParts) 1) -}}
            {{- $results = append $results $fullPath -}}
          {{- end -}}
        {{- end -}}

        {{- /* If directory, recurse into it */ -}}
        {{- if $entry.IsDir -}}
          {{- $subResults := include "globRecursive" (dict "dir" $fullPath "pattern" $pattern "prefix" $prefix) | fromJson -}}
          {{- range $subResults -}}
            {{- $results = append $results . -}}
          {{- end -}}
        {{- end -}}
      {{- else -}}
        {{- /* No pattern after **, match everything */ -}}
        {{- $results = append $results $fullPath -}}

        {{- /* Recurse into subdirectories */ -}}
        {{- if $entry.IsDir -}}
          {{- $subResults := include "globRecursive" (dict "dir" $fullPath "pattern" "" "prefix" $prefix) | fromJson -}}
          {{- range $subResults -}}
            {{- $results = append $results . -}}
          {{- end -}}
        {{- end -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}

  {{- $results | toJson -}}
{{- end -}}
