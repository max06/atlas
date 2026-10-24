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
{{- tpl (include "atlas.deployment.source" .) $ctx }}
{{- end -}}

{{- /*
  atlas.deployment.expanded — a deployment definition with its macros expanded.

  A macro (deploymentTemplate) is a template directory with a
  deployment.yaml.gotmpl instead of a helmfile.yaml.gotmpl. A deployment uses
  it under the key deploymentTemplates:

    deploymentTemplates:
      - template: web-service
        values: {name: shop, dbConnectionPoolSize: 200}   # a map or a list of maps

  The macro file renders (Go template) with the state-build hierarchy of the
  pair (no secrets) and the macro values on top (.Values.<key>). It returns
  an apps list. Each entry becomes one component of the deployment:

    apps:
      - template: cnpg-cluster
        name: db                 # entry name; default: base name of the template
        destination: mgmt        # optional: Argo CD destination of the component
        needs: [other-entry]     # optional: order inside the deployment
        values: [...]

  Entries of the deployment's own apps list come after the macro entries and
  become components as well. A component is named <deployment>-<entry name>.
  It renders as a child (recursive ATLAS) with a definition of its one app and
  the deployment's settings, and the deployment directory as its base.

  A deployment with macros renders, in its own Application, only the parent
  template (.Values.atlas.macroParentTemplate, set by the consumer's entry): a
  structure template that reads the components (atlas.deployment.components)
  and renders their Applications.

  Input:  dict with "Values" (atlas sub-context with atlas.deployment.*),
          optional "hierarchy" (state-build hierarchy, as for
          atlas.deployment.definition)
  Output: JSON {
            "definition": <the parsed deployment.yaml>,
            "macro":      true when the deployment uses deploymentTemplates,
            "macros":     [<macro template names>],
            "renderApps": [<apps this render instantiates>],
            "allApps":    [<every app the deployment and its components use>],
            "components": [{"name", "entry", "destination", "needs"}]
          }
*/ -}}
{{- define "atlas.deployment.expanded" -}}
{{- $synthRelease := dict "Name" "" "Namespace" "" }}
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
{{- $definition := include "atlas.deployment.definition" (dict "Values" .Values "hierarchy" $hierarchy) | fromYaml }}
{{- $macroUses := $definition | get "deploymentTemplates" list }}
{{- $ownApps := $definition | get "apps" list }}
{{- if not $macroUses }}
  {{- dict "definition" $definition "macro" false "macros" (list) "renderApps" $ownApps "allApps" $ownApps "components" (list) | toJson }}
{{- else }}
  {{- $deploymentName := .Values.atlas.deployment.deploymentName }}
  {{- $parentTemplate := .Values.atlas | get "macroParentTemplate" "" }}
  {{- if not $parentTemplate }}
    {{- fail (printf "deployment %s uses deploymentTemplates, but the entry helmfile sets no atlas.macroParentTemplate (the structure template that renders the components)" $deploymentName) }}
  {{- end }}
  {{- $entries := list }}
  {{- $macroNames := list }}
  {{- range $use := $macroUses }}
    {{- $macroName := required (printf "deployment %s: deploymentTemplates[].template" $deploymentName) ($use | get "template" "") }}
    {{- $macroNames = append $macroNames $macroName }}
    {{- $macroFile := printf "%s/%s/%s/deployment.yaml.gotmpl" $.Values.atlas.cwd $.Values.atlas.appTemplates $macroName }}
    {{- if not (isFile $macroFile) }}
      {{- fail (printf "deployment %s: macro %s not found (expected %s)" $deploymentName $macroName $macroFile) }}
    {{- end }}
    {{- /* macro values: a map, or a list of maps merged in order */ -}}
    {{- $macroValues := dict }}
    {{- $rawValues := $use | get "values" dict }}
    {{- if kindIs "slice" $rawValues }}
      {{- range $part := $rawValues }}{{- $macroValues = mergeOverwrite $macroValues (deepCopy $part) }}{{- end }}
    {{- else }}
      {{- $macroValues = deepCopy $rawValues }}
    {{- end }}
    {{- $macroCtx := mergeOverwrite (deepCopy $.Values) (deepCopy $hierarchy) (deepCopy $macroValues) }}
    {{- $_ := set $macroCtx "Release" $synthRelease }}
    {{- $_ := set $macroCtx "Values" (deepCopy $macroCtx) }}
    {{- $expansion := tpl (readFile $macroFile) $macroCtx | fromYaml }}
    {{- $entries = concat $entries ($expansion | get "apps" list) }}
  {{- end }}
  {{- $entries = concat $entries $ownApps }}
  {{- /* entry names: name, else the base name of the template; unique */ -}}
  {{- $entryNames := list }}
  {{- range $entry := $entries }}
    {{- $entryName := $entry | get "name" (base ($entry | get "template" "")) }}
    {{- if has $entryName $entryNames }}
      {{- fail (printf "deployment %s: two macro entries with the name %q — set name on one of them" $deploymentName $entryName) }}
    {{- end }}
    {{- $entryNames = append $entryNames $entryName }}
  {{- end }}
  {{- $components := list }}
  {{- $allApps := list (dict "template" $parentTemplate) }}
  {{- range $entry := $entries }}
    {{- $entryName := $entry | get "name" (base ($entry | get "template" "")) }}
    {{- $needs := list }}
    {{- range $need := ($entry | get "needs" list) }}
      {{- if not (has $need $entryNames) }}
        {{- fail (printf "deployment %s: entry %s needs %q, which is no entry of this deployment" $deploymentName $entryName $need) }}
      {{- end }}
      {{- $needs = append $needs (printf "%s-%s" $deploymentName $need) }}
    {{- end }}
    {{- $app := omit $entry "destination" "needs" }}
    {{- $allApps = append $allApps $app }}
    {{- $components = append $components (dict "name" (printf "%s-%s" $deploymentName $entryName) "entry" $app "destination" ($entry | get "destination" "") "needs" $needs) }}
  {{- end }}
  {{- dict "definition" $definition "macro" true "macros" $macroNames "renderApps" (list (dict "template" $parentTemplate)) "allApps" $allApps "components" $components | toJson }}
{{- end }}
{{- end -}}

{{- /*
  atlas.deployment.components — the components of a deployment with macros,
  for its parent template (a structure template).

  Each component is ready for a child render: cluster and base are the
  deployment's (the component keeps the hierarchy of its cluster, whatever its
  destination), the definition holds the one app and the deployment's
  settings.

  Input:  dict with "Values" (atlas sub-context of the deployment)
  Output: JSON {"v": [{"name", "cluster", "base", "definition" (YAML text),
          "destination", "needs"}, ...]}; [] for a deployment without macros.
*/ -}}
{{- define "atlas.deployment.components" -}}
{{- $expanded := include "atlas.deployment.expanded" (dict "Values" .Values) | fromJson }}
{{- $base := dir .Values.atlas.deployment.deploymentPath | trimPrefix (printf "%s/" .Values.atlas.cwd) }}
{{- $settings := $expanded.definition | get "settings" dict }}
{{- $out := list }}
{{- range $component := $expanded.components }}
  {{- $componentSettings := deepCopy $settings }}
  {{- if $component.needs }}{{- $_ := set $componentSettings "needs" $component.needs }}{{- end }}
  {{- $childDefinition := dict "apps" (list $component.entry) "settings" $componentSettings }}
  {{- $out = append $out (dict
        "name"        $component.name
        "cluster"     $.Values.atlas.deployment.cluster
        "base"        $base
        "definition"  ($childDefinition | toYaml)
        "destination" $component.destination
        "needs"       $component.needs) }}
{{- end }}
{{- dict "v" $out | toJson }}
{{- end -}}

{{- /*
  atlas.cluster.groups — the group levels above one cluster, outer first.

  A cluster path is group/.../group/leaf. Every directory on the way from the
  root to the cluster is a group level: it can hold group.values.* files and
  an apps/ directory whose deployments all clusters below it inherit.

    "region/eu/c1" -> ["region", "region/eu"]
    "group1/c2"    -> ["group1"]
    "c3"           -> []

  Before nested groups, ATLAS used `dir <cluster>` as the one group level
  ("region/eu"). For one group level both give the same result.

  Input:  the cluster path (string)
  Output: JSON {"v": [<group path>, ...]}; callers read .v after fromJson.
*/ -}}
{{- define "atlas.cluster.groups" -}}
{{- $groupPaths := list }}
{{- $prefix := "" }}
{{- range $part := (splitList "/" .) | initial }}
  {{- if $prefix }}
    {{- $prefix = printf "%s/%s" $prefix $part }}
  {{- else }}
    {{- $prefix = $part }}
  {{- end }}
  {{- $groupPaths = append $groupPaths $prefix }}
{{- end }}
{{- dict "v" $groupPaths | toJson }}
{{- end -}}

{{- /*
  atlas.cluster.deployments — the deployments of one cluster.

  Collects <level>/apps/<name>/deployment.yaml from every level above and at the
  cluster: global, each group level (outer first, atlas.cluster.groups), the
  cluster. When the same deployment name appears at several levels, the most
  specific one wins (later levels override earlier ones).

  The discovery (helmfile.all.yaml.gotmpl, STEP 2) and templates use this one
  helper. A structure template (for example the cluster root of a static
  ApplicationSet) calls it to render one Application per deployment; the
  result never goes into .Values.atlas, so charts see no difference.

  Input:  dict with
            "Values"   needs .Values.atlas.cwd and .Values.atlas.deploymentDefinitions
            "cluster"  the cluster path
  Output: JSON {"v": [{"deploymentName": <name>, "path": <absolute deployment.yaml>}, ...]},
          sorted by name.
*/ -}}
{{- define "atlas.cluster.deployments" -}}
{{- $deploymentsDir := printf "%s/%s" .Values.atlas.cwd .Values.atlas.deploymentDefinitions }}
{{- $levels := list "" }}
{{- range $groupPath := (include "atlas.cluster.groups" .cluster | fromJson).v }}
  {{- $levels = append $levels (printf "%s/" $groupPath) }}
{{- end }}
{{- $levels = append $levels (printf "%s/" .cluster) }}
{{- $byName := dict }}
{{- range $level := $levels }}
  {{- range (include "glob" (printf "%s/%sapps/*/deployment.yaml" $deploymentsDir $level) | fromJson) }}
    {{- $_ := set $byName (. | dir | base) . }}
  {{- end }}
{{- end }}
{{- $deployments := list }}
{{- range $name := keys $byName | sortAlpha }}
  {{- $deployments = append $deployments (dict "deploymentName" $name "path" (index $byName $name)) }}
{{- end }}
{{- dict "v" $deployments | toJson }}
{{- end -}}

{{- /*
  atlas.child.deploymentPath — the deployment.yaml path of a child render.

  A child render (ATLAS_DEPLOYMENT set, see atlas.deployment.source) has no
  deployment.yaml on disk. Its deploymentPath is
  <cwd>/<ATLAS_DEPLOYMENT_BASE>/deployment.yaml: a path that may not exist,
  but whose directory is the base of the deployment-level files
  (values.yaml, values.yaml.gotmpl, values.sops.yaml). The values loader
  reads that directory through `dir deploymentPath`, as for a file-based
  deployment. Thus a child whose base is the directory of an existing
  deployment renders exactly like that deployment.

  Input:  dict with "Values" (needs .Values.atlas.cwd)
  Output: the absolute path, or "" when ATLAS_DEPLOYMENT_BASE is unset.
*/ -}}
{{- define "atlas.child.deploymentPath" -}}
{{- with env "ATLAS_DEPLOYMENT_BASE" | trim | trimSuffix "/" }}
  {{- clean (printf "%s/%s/deployment.yaml" $.Values.atlas.cwd .) }}
{{- end }}
{{- end -}}

{{- /*
  atlas.deployment.source — the raw (untemplated) text of one deployment
  definition.

  File-based deployments: the content of .Values.atlas.deployment.deploymentPath.

  Child render: when the environment variable ATLAS_DEPLOYMENT holds a
  definition (YAML or JSON, the same content a deployment.yaml would have)
  and the current deploymentPath is the child path (atlas.child.deploymentPath),
  the definition comes from the variable instead of the file system. The
  atlas plugin in Argo CD passes it as plugin env; a parent deployment
  renders the child Application with it (recursive ATLAS).

  Why the variable is read here, at every stage, and not passed through
  .Values.atlas: .Values.atlas reaches the charts. A definition in it would
  change the values of every child render, and a child render must be
  identical to the render of the same deployment from a file.

  Input:  dict with "Values" (.Values.atlas.deployment.deploymentPath, .Values.atlas.cwd)
  Output: the definition text. Fails when a file-based definition is missing.
*/ -}}
{{- define "atlas.deployment.source" -}}
{{- $deploymentPath := .Values.atlas.deployment.deploymentPath }}
{{- $childDefinition := env "ATLAS_DEPLOYMENT" }}
{{- if and $childDefinition (eq $deploymentPath (include "atlas.child.deploymentPath" .)) }}
  {{- $childDefinition }}
{{- else }}
  {{- if not (isFile $deploymentPath) }}
    {{- fail (printf "Deployment file not found: %s" $deploymentPath) }}
  {{- end }}
  {{- readFile $deploymentPath }}
{{- end }}
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
  in OVER the hierarchy, matching the final value precedence the loader
  establishes: the instance is the most specific layer.

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
