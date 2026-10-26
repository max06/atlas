package engine

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Instance is the state of one app instance of one pair: what
// helmfile.instance.yaml.gotmpl emits, with the values of each release
// computed in Go instead of the values loader.
type Instance struct {
	Pair     Pair
	Template string         // atlas.instance.template
	Name     string         // atlas.instance.name
	Values   map[string]any // .Values of the state (the atlas object)
	// State is the emitted state document. Each release's values: list
	// ends with ValuesRef, the place of the values loader.
	State map[string]any
	// Releases are the releases of State, in order (the maps inside State).
	Releases []map[string]any
}

// ValuesRef is the placeholder for the values of a release in
// Instance.State; the caller replaces it with the path of the file that
// holds ReleaseValues.
const ValuesRef = "atlas-native-values"

// Instances is helmfile.single.yaml.gotmpl (the fan-out) and
// helmfile.instance.yaml.gotmpl (the state of each instance) for one pair.
func (e *Engine) Instances(p Pair) ([]*Instance, error) {
	values, err := roundTrip(e.pairValues(p), "atlas")
	if err != nil {
		return nil, err
	}
	definition, err := e.Definition(values, nil)
	if err != nil {
		return nil, err
	}
	apps, err := field(definition, "apps", p.DeploymentPath)
	if err != nil {
		return nil, err
	}
	appList, err := rangeValues(apps, p.DeploymentPath+": apps")
	if err != nil {
		return nil, err
	}
	var out []*Instance
	for _, app := range appList {
		appMap, _ := app.(map[string]any)
		template, err := field(app, "template", p.DeploymentPath+": apps[]")
		if err != nil {
			return nil, err
		}
		name := template
		if hasKey(appMap, "name") {
			name = appMap["name"]
		}
		atlas := deepCopy(values["atlas"].(map[string]any))
		atlas["instance"] = map[string]any{"template": template, "name": name}
		// the state values travel as YAML (helmfiles[].values)
		instanceValues, err := roundTrip(map[string]any{"atlas": atlas}, "atlas")
		if err != nil {
			return nil, err
		}
		inst, err := e.instanceState(p, instanceValues)
		if err != nil {
			return nil, fmt.Errorf("%s/%s instance %v: %w", p.Cluster, p.DeploymentName, name, err)
		}
		out = append(out, inst)
	}
	return out, nil
}

func (e *Engine) instanceState(p Pair, values map[string]any) (*Instance, error) {
	atlas := values["atlas"].(map[string]any)
	inst := atlas["instance"].(map[string]any)
	templateDir := fmt.Sprintf("%s/%s/%s", atlas["cwd"], atlas["appTemplates"], inst["template"])
	templateFile := templateDir + "/helmfile.yaml.gotmpl"
	if ok, err := isFile(templateFile); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("App template not found: %s", templateFile)
	}
	hierarchy, err := e.stateHierarchy(values)
	if err != nil {
		return nil, err
	}
	ctx := merge(deepCopy(values), deepCopy(hierarchy))
	ctx["Release"] = synthRelease()
	ctx["Values"] = deepCopy(ctx)
	deploymentPath := p.DeploymentPath
	text, err := readText(deploymentPath)
	if err != nil {
		return nil, err
	}
	out, err := e.T.Tpl(text, ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", deploymentPath, err)
	}
	definition, err := fromYamlMap(out, deploymentPath)
	if err != nil {
		return nil, err
	}
	this, err := instanceOf(definition, inst["template"], inst["name"], deploymentPath)
	if err != nil {
		return nil, err
	}
	rendered, err := e.InstanceTemplate(values, hierarchy, this)
	if err != nil {
		return nil, err
	}
	template, err := fromYamlMap(rendered, templateFile)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, fmt.Errorf("%s: the template renders no state", templateFile)
	}

	same, err := tEq(inst["name"], inst["template"])
	if err != nil {
		return nil, err
	}
	needsMunge := !same
	var nameStyle any = "prefix"
	if needsMunge && hasKey(this, "nameStyle") {
		nameStyle = this["nameStyle"]
		if nameStyle != "prefix" && nameStyle != "suffix" {
			return nil, fmt.Errorf("apps[].nameStyle must be 'prefix' or 'suffix', got: %q (template=%s instance=%s)", nameStyle, inst["template"], inst["name"])
		}
	}
	if atlas["redactSecrets"] == true || atlas["redactMaps"] == true {
		return nil, fmt.Errorf("native engine: redaction is not implemented yet")
	}

	releases, err := field(template, "releases", templateFile)
	if err != nil {
		return nil, err
	}
	releaseList, err := rangeValues(releases, templateFile+": releases")
	if err != nil {
		return nil, err
	}
	result := &Instance{Pair: p, Template: fmt.Sprintf("%v", inst["template"]), Name: fmt.Sprintf("%v", inst["name"]), Values: values, State: template}
	deploymentDir := path.Dir(fmt.Sprintf("%s", atlas["deployment"].(map[string]any)["deploymentPath"]))
	for _, r := range releaseList {
		release, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: a release is not a map", templateFile)
		}
		if hasKey(release, "postRenderer") {
			return nil, fmt.Errorf("release %q sets postRenderer (%v) — ATLAS owns this slot for its redaction post-renderer and helm 4 cannot chain post-renderers. Open an issue at https://github.com/max06/atlas to discuss your use case (template=%s instance=%s).", release["name"], release["postRenderer"], inst["template"], inst["name"])
		}
		settingsV, err := get(definition, "settings", map[string]any{})
		if err != nil {
			return nil, err
		}
		settings, _ := settingsV.(map[string]any)
		if hasKey(this, "namespace") {
			release["namespace"] = this["namespace"]
		} else if hasKey(settings, "namespace") {
			release["namespace"] = settings["namespace"]
		}
		if hasKey(release, "chart") {
			chart, err := rawString(release["chart"], "release chart")
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(chart, "./") || strings.HasPrefix(chart, "../") {
				release["chart"] = templateDir + "/" + chart
			}
		}
		releaseValues, err := list(release, "values")
		if err != nil {
			return nil, err
		}
		preserved := []any{}
		for _, v := range releaseValues {
			if kindIsMap(v) {
				preserved = append(preserved, v)
			}
		}
		release["values"] = append(preserved, ValuesRef)
		delete(release, "condition")
		delete(release, "installed")
		delete(release, "secrets")
		if !hasKey(release, "skipSchemaValidation") {
			release["skipSchemaValidation"] = true
		}
		if hasKey(this, "labels") {
			current, err := get(release, "labels", map[string]any{})
			if err != nil {
				return nil, err
			}
			cm, err := asMap(current, "release labels")
			if err != nil {
				return nil, err
			}
			lm, err := asMap(this["labels"], "apps[].labels")
			if err != nil {
				return nil, err
			}
			release["labels"] = merge(cm, lm)
		}
		for _, f := range []string{"strategicMergePatches", "jsonPatches", "transformers"} {
			if err := applyListOverride(release, this, templateDir, deploymentDir, f); err != nil {
				return nil, err
			}
		}
		if hasKey(release, "keyring") {
			kr, err := rawString(release["keyring"], "release keyring")
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(kr, "./") || strings.HasPrefix(kr, "../") {
				release["keyring"] = templateDir + "/" + kr
			}
		}
		for _, f := range []string{"set", "setString"} {
			if !hasKey(release, f) {
				continue
			}
			entries, err := list(release, f)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				m, ok := entry.(map[string]any)
				if !ok || !hasKey(m, "file") {
					continue
				}
				file, err := rawString(m["file"], "release "+f+"[].file")
				if err != nil {
					return nil, err
				}
				if strings.HasPrefix(file, "./") || strings.HasPrefix(file, "../") {
					m["file"] = templateDir + "/" + file
				}
			}
		}
		if needsMunge {
			release["name"] = munged(release["name"], inst["name"], true, nameStyle)
		}
		result.Releases = append(result.Releases, release)
	}

	deployment := atlas["deployment"].(map[string]any)
	labels := map[string]any{
		"cluster":        deployment["cluster"],
		"clusterName":    deployment["clusterName"],
		"deploymentName": deployment["deploymentName"],
		"template":       inst["template"],
		"instance":       inst["name"],
	}
	if hasKey(deployment, "clusterGroup") {
		labels["clusterGroup"] = deployment["clusterGroup"]
	}
	template["commonLabels"] = labels
	template["environments"] = map[string]any{"default": map[string]any{"values": []any{map[string]any{"atlas": atlas}}}}
	return result, nil
}

// applyListOverride is atlas.applyListOverride: template-relative paths of
// a release list field made absolute, then the instance's entries
// (deployment-relative) appended.
func applyListOverride(release, instance map[string]any, templateDir, deploymentDir, field string) error {
	if hasKey(release, field) {
		val, err := get(release, field, nil)
		if err != nil {
			return err
		}
		if truthy(val) {
			converted, err := convertPaths(templateDir, val, field)
			if err != nil {
				return err
			}
			release[field] = converted
		}
	}
	if hasKey(instance, field) {
		add, err := get(instance, field, []any{})
		if err != nil {
			return err
		}
		if truthy(add) {
			converted, err := convertPaths(deploymentDir, add, field+" (instance-level)")
			if err != nil {
				return err
			}
			current, err := get(release, field, []any{})
			if err != nil {
				return err
			}
			cl, _ := current.([]any)
			release[field] = append(append([]any{}, cl...), converted...)
		}
	}
	return nil
}

// convertPaths is the template's convertPaths: string entries become
// target-relative absolute paths and must exist; other entries pass. The
// template hands the list through JSON (toJson / fromJson).
func convertPaths(target string, values any, field string) ([]any, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	var entries []any
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	out := []any{}
	for _, entry := range entries {
		s, ok := entry.(string)
		if !ok {
			out = append(out, entry)
			continue
		}
		abs := target + "/" + s
		if ok, err := isFile(abs); err != nil {
			return nil, err
		} else if !ok {
			return nil, fmt.Errorf("%s: file not found: %s/%s", field, target, s)
		}
		out = append(out, abs)
	}
	// the result travels back through toJson | fromJson
	data, err = json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var back []any
	if err := json.Unmarshal(data, &back); err != nil {
		return nil, err
	}
	return back, nil
}

// ReleaseContext is .Release of a release at release time: what helmfile
// hands to values templates (Name and Namespace are what the trees use).
func ReleaseContext(release map[string]any, namespaceOverride string) map[string]any {
	ns := fmt.Sprintf("%v", nilToEmpty(release["namespace"]))
	if namespaceOverride != "" {
		ns = namespaceOverride
	}
	labels := map[string]any{}
	if l, ok := release["labels"].(map[string]any); ok {
		for k, v := range l {
			labels[k] = fmt.Sprintf("%v", v)
		}
	}
	return map[string]any{
		"Name":         fmt.Sprintf("%v", nilToEmpty(release["name"])),
		"Namespace":    ns,
		"Labels":       labels,
		"Chart":        fmt.Sprintf("%v", nilToEmpty(release["chart"])),
		"ChartVersion": fmt.Sprintf("%v", nilToEmpty(release["version"])),
		"KubeContext":  fmt.Sprintf("%v", nilToEmpty(release["kubeContext"])),
	}
}

func nilToEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}
