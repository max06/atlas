package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Engine is one native render context.
type Engine struct {
	Layout Layout
	// Atlas is the atlas object the entry hands to the templates: cwd,
	// appTemplates, deploymentDefinitions and the runtime switches
	// (redactSecrets, skipSecrets, redactMaps), without filter and
	// discoveryMap.
	Atlas map[string]any
	T     *Templater
	Sops  Sops
}

// Switches are the runtime switches of the entry (ATLAS_* variables).
type Switches struct {
	RedactSecrets, SkipSecrets, RedactMaps bool
}

// New builds an engine for a layout.
func New(l Layout, s Switches) *Engine {
	return &Engine{
		Layout: l,
		Atlas: map[string]any{
			"cwd":                   l.Root,
			"appTemplates":          l.Templates,
			"deploymentDefinitions": l.Deployments,
			"redactSecrets":         s.RedactSecrets,
			"skipSecrets":           s.SkipSecrets,
			"redactMaps":            s.RedactMaps,
		},
		T: NewTemplater(),
	}
}

// pairValues is .Values of a pair's states: the atlas object with
// atlas.deployment of the pair.
func (e *Engine) pairValues(p Pair) map[string]any {
	atlas := deepCopy(e.Atlas)
	d := map[string]any{
		"cluster":        p.Cluster,
		"clusterName":    p.ClusterName,
		"deploymentName": p.DeploymentName,
		"deploymentPath": p.DeploymentPath,
	}
	if p.ClusterGroup != "" {
		d["clusterGroup"] = p.ClusterGroup
	}
	atlas["deployment"] = d
	return map[string]any{"atlas": atlas}
}

// stateHierarchy is the state-build walk: skipSecrets, synthetic .Release.
func (e *Engine) stateHierarchy(values map[string]any) (map[string]any, error) {
	return e.Hierarchy(Context{Values: values, Release: synthRelease(), SkipSecrets: true})
}

// Definition is atlas.deployment.definition: the deployment.yaml rendered
// with the state-build hierarchy in scope.
func (e *Engine) Definition(values, hierarchy map[string]any) (map[string]any, error) {
	atlas, err := atlasOf(values)
	if err != nil {
		return nil, err
	}
	deployment, _ := atlas["deployment"].(map[string]any)
	file := fmt.Sprintf("%s", deployment["deploymentPath"])
	if ok, err := isFile(file); err != nil || !ok {
		return nil, fmt.Errorf("Deployment file not found: %s", file)
	}
	if hierarchy == nil {
		if hierarchy, err = e.stateHierarchy(values); err != nil {
			return nil, err
		}
	}
	ctx := merge(deepCopy(values), deepCopy(hierarchy))
	ctx["Release"] = synthRelease()
	ctx["Values"] = deepCopy(ctx)
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	out, err := e.T.Tpl(string(data), ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return fromYamlMap(out, file)
}

// InstanceTemplate is atlas.instance.template: the app template of one
// instance rendered for the state build (hierarchy, synthetic .Release,
// the instance's inline map values over both).
func (e *Engine) InstanceTemplate(values, hierarchy, instance map[string]any) (string, error) {
	atlas, err := atlasOf(values)
	if err != nil {
		return "", err
	}
	inst, _ := atlas["instance"].(map[string]any)
	file := fmt.Sprintf("%s/%s/%s/helmfile.yaml.gotmpl", atlas["cwd"], atlas["appTemplates"], inst["template"])
	ctx := merge(deepCopy(values), deepCopy(hierarchy))
	ctx["Release"] = synthRelease()
	ctx["Values"] = deepCopy(ctx)
	entries, err := list(instance, "values")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if kindIsMap(entry) {
			m, err := asMap(entry, "instance values")
			if err != nil {
				return "", err
			}
			ctx = merge(ctx, m)
		}
	}
	ctx["Values"] = deepCopy(ctx)
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	out, err := e.T.Tpl(string(data), ctx)
	if err != nil {
		return "", fmt.Errorf("%s: %w", file, err)
	}
	return out, nil
}

// localDependencies is atlas.chart.localDependencies: the file:// subcharts
// of a chart directory, recursively, without the ones in seen.
func localDependencies(dir string, seen []string) ([]string, error) {
	found := []string{}
	chartFile := dir + "/Chart.yaml"
	ok, err := isFile(chartFile)
	if err != nil || !ok {
		return found, err
	}
	data, err := os.ReadFile(chartFile)
	if err != nil {
		return nil, err
	}
	chart, err := fromYamlMap(string(data), chartFile)
	if err != nil {
		return nil, err
	}
	deps, err := list(chart, "dependencies")
	if err != nil {
		return nil, err
	}
	for _, dep := range deps {
		repo, err := str(dep, "repository", "")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(repo, "file://") {
			continue
		}
		target := strings.TrimPrefix(repo, "file://")
		if !filepath.IsAbs(target) {
			target = dir + "/" + target
		}
		target = filepath.Clean(target)
		ok, err := isDir(target)
		if err != nil {
			return nil, err
		}
		if !ok || has(seen, target) || has(found, target) {
			continue
		}
		found = append(found, target)
		nested, err := localDependencies(target, append(append([]string{}, seen...), found...))
		if err != nil {
			return nil, err
		}
		for _, n := range nested {
			if !has(found, n) {
				found = append(found, n)
			}
		}
	}
	return found, nil
}

// pairTemplatesAndCharts is the discovery map part of one pair: the app
// templates the deployment instantiates and the repository-relative local
// chart directories their releases render from.
func (e *Engine) pairTemplatesAndCharts(p Pair) ([]string, []string, error) {
	values := e.pairValues(p)
	hierarchy, err := e.stateHierarchy(values)
	if err != nil {
		return nil, nil, err
	}
	definition, err := e.Definition(values, hierarchy)
	if err != nil {
		return nil, nil, err
	}
	apps, err := list(definition, "apps")
	if err != nil {
		return nil, nil, err
	}
	root := e.Layout.Root
	var templateNames, charts []any
	for _, app := range apps {
		templateName, err := get(app, "template", "")
		if err != nil {
			return nil, nil, err
		}
		templateNames = append(templateNames, templateName)
		templateDir := fmt.Sprintf("%s/%s/%s", root, e.Atlas["appTemplates"], templateName)
		// `and` stops at the first false operand: no stat without a name
		if !truthy(templateName) {
			continue
		}
		if ok, err := isFile(templateDir + "/helmfile.yaml.gotmpl"); err != nil {
			return nil, nil, err
		} else if !ok {
			continue // a missing template is the real render's error
		}
		appMap, err := asMap(app, "apps entry")
		if err != nil {
			return nil, nil, err
		}
		name, err := get(app, "name", templateName)
		if err != nil {
			return nil, nil, err
		}
		instanceValues := deepCopy(values)
		instanceValues["atlas"].(map[string]any)["instance"] = map[string]any{"template": templateName, "name": name}
		out, err := e.InstanceTemplate(instanceValues, hierarchy, appMap)
		if err != nil {
			return nil, nil, err
		}
		rendered, err := fromYamlMap(out, templateDir+"/helmfile.yaml.gotmpl")
		if err != nil {
			return nil, nil, err
		}
		releases, err := list(rendered, "releases")
		if err != nil {
			return nil, nil, err
		}
		for _, release := range releases {
			chart, err := str(release, "chart", "")
			if err != nil {
				return nil, nil, err
			}
			if strings.HasPrefix(chart, "./") || strings.HasPrefix(chart, "../") {
				chart = templateDir + "/" + chart
			}
			if !filepath.IsAbs(chart) {
				continue
			}
			chart = filepath.Clean(chart)
			if !strings.HasPrefix(chart, root+"/") {
				continue
			}
			if ok, err := isDir(chart); err != nil {
				return nil, nil, err
			} else if !ok {
				continue
			}
			deps, err := localDependencies(chart, []string{chart})
			if err != nil {
				return nil, nil, err
			}
			for _, d := range append([]string{chart}, deps...) {
				charts = append(charts, strings.TrimPrefix(d, root+"/"))
			}
		}
	}
	return sortAlpha(uniqFn(templateNames)), sortAlpha(uniqFn(charts)), nil
}

// truthy is the template's truth of a value (`if $x`).
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	}
	return true
}
