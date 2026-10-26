package engine

import (
	"fmt"
	"os"
	"path"
	"reflect"
	"strings"
)

// field is `$m.key` in a template with missingkey=error: a missing key (or
// a nil map) is an error.
func field(m any, key, what string) (any, error) {
	mm, ok := m.(map[string]any)
	if !ok || mm == nil {
		return nil, fmt.Errorf("%s: nil pointer evaluating interface {}.%s", what, key)
	}
	v, ok := mm[key]
	if !ok {
		return nil, fmt.Errorf("%s: map has no entry for key %q", what, key)
	}
	return v, nil
}

// hasKey is sprig's hasKey.
func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// tEq is the template's `eq` for two operands: equal basic values; values
// of different basic kinds cannot be compared.
func tEq(a, b any) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	ka, kb := basicKind(va), basicKind(vb)
	if ka != kb {
		return false, fmt.Errorf("incompatible types for comparison")
	}
	switch ka {
	case "string":
		return va.String() == vb.String(), nil
	case "int":
		return va.Int() == vb.Int(), nil
	case "uint":
		return va.Uint() == vb.Uint(), nil
	case "float":
		return va.Float() == vb.Float(), nil
	case "bool":
		return va.Bool() == vb.Bool(), nil
	}
	return false, fmt.Errorf("invalid type for comparison")
}

func basicKind(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "uint"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Bool:
		return "bool"
	}
	return v.Kind().String()
}

func isString(v any) bool { _, ok := v.(string); return ok }

// instanceOf finds the apps[] entry of the instance in a rendered
// deployment.yaml (template + name; the name defaults to the template).
// The last match wins, like the template's range.
func instanceOf(definition map[string]any, template, name any, what string) (map[string]any, error) {
	apps, err := field(definition, "apps", what)
	if err != nil {
		return nil, err
	}
	appList, err := rangeValues(apps, what+": apps")
	if err != nil {
		return nil, err
	}
	this := map[string]any{}
	for _, app := range appList {
		appTemplate, err := field(app, "template", what+": apps[]")
		if err != nil {
			return nil, err
		}
		instanceName := appTemplate
		if m, _ := app.(map[string]any); hasKey(m, "name") {
			instanceName = m["name"]
		}
		t, err := tEq(appTemplate, template)
		if err != nil {
			return nil, err
		}
		if !t {
			continue
		}
		n, err := tEq(instanceName, name)
		if err != nil {
			return nil, err
		}
		if n {
			this = app.(map[string]any)
		}
	}
	return this, nil
}

// munged is the release name after the instance munging.
func munged(name any, instanceName any, needsMunge bool, nameStyle any) string {
	if !needsMunge {
		return fmt.Sprintf("%v", name)
	}
	if s, _ := nameStyle.(string); s == "suffix" {
		return fmt.Sprintf("%s-%s", name, instanceName)
	}
	return fmt.Sprintf("%s-%s", instanceName, name)
}

func readText(file string) (string, error) {
	data, err := os.ReadFile(file)
	return string(data), err
}

// valuesFile loads one entry of a values: list (a path), by kind.
func (e *Engine) valuesFile(abs, entry string, redact, skipSecrets bool, progCtx func() (map[string]any, error)) (map[string]any, error) {
	switch {
	case strings.HasSuffix(entry, ".sops.yaml"):
		return e.sopsLoad(abs, redact, skipSecrets)
	case strings.HasSuffix(entry, ".yaml.gotmpl"):
		ctx, err := progCtx()
		if err != nil {
			return nil, err
		}
		text, err := readText(abs)
		if err != nil {
			return nil, err
		}
		out, err := e.T.Tpl(text, ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", abs, err)
		}
		return fromYamlMap(out, abs)
	}
	text, err := readText(abs)
	if err != nil {
		return nil, err
	}
	return fromYamlMap(text, abs)
}

// Values is atlas.values.merged: the values of one release at release
// time (chart < template values < template secrets < hierarchy < instance
// values < instance secrets). values is the instance state's .Values
// (atlas with deployment and instance), release is .Release.
func (e *Engine) Values(values, release map[string]any, redact bool) (map[string]any, error) {
	atlas, err := atlasOf(values)
	if err != nil {
		return nil, err
	}
	inst, _ := atlas["instance"].(map[string]any)
	deployment, _ := atlas["deployment"].(map[string]any)
	templateDir := fmt.Sprintf("%s/%s/%s", atlas["cwd"], atlas["appTemplates"], inst["template"])
	templateFile := templateDir + "/helmfile.yaml.gotmpl"
	deploymentPath := fmt.Sprintf("%s", deployment["deploymentPath"])
	deploymentDir := path.Dir(deploymentPath)
	skip, err := get(values, "atlas.skipSecrets", false)
	if err != nil {
		return nil, err
	}
	skipSecrets := truthy(skip)

	hierarchy, err := e.Hierarchy(Context{Values: values, Release: release, Redact: redact, SkipSecrets: skipSecrets})
	if err != nil {
		return nil, err
	}

	ctx := merge(deepCopy(values), deepCopy(hierarchy))
	ctx["Release"] = release
	ctx["Values"] = deepCopy(ctx)

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
	instanceName := inst["name"]
	needsMunge, err := tEq(instanceName, inst["template"])
	if err != nil {
		return nil, err
	}
	needsMunge = !needsMunge
	this, err := instanceOf(definition, inst["template"], instanceName, deploymentPath)
	if err != nil {
		return nil, err
	}
	var nameStyle any = "prefix"
	if needsMunge && hasKey(this, "nameStyle") {
		nameStyle = this["nameStyle"]
	}

	instanceValues, err := list(this, "values")
	if err != nil {
		return nil, err
	}
	for _, entry := range instanceValues {
		if kindIsMap(entry) {
			ctx = merge(ctx, entry.(map[string]any))
		}
	}
	ctx["Values"] = deepCopy(ctx)

	if text, err = readText(templateFile); err != nil {
		return nil, err
	}
	if out, err = e.T.Tpl(text, ctx); err != nil {
		return nil, fmt.Errorf("%s: %w", templateFile, err)
	}
	rendered, err := fromYamlMap(out, templateFile)
	if err != nil {
		return nil, err
	}
	releases, err := field(rendered, "releases", templateFile)
	if err != nil {
		return nil, err
	}
	releaseList, err := rangeValues(releases, templateFile+": releases")
	if err != nil {
		return nil, err
	}
	thisRelease := map[string]any{}
	for _, rel := range releaseList {
		name, err := field(rel, "name", templateFile+": releases[]")
		if err != nil {
			return nil, err
		}
		if ok, err := tEq(munged(name, instanceName, needsMunge, nameStyle), release["Name"]); err != nil {
			return nil, err
		} else if ok {
			thisRelease = rel.(map[string]any)
		}
	}

	// shares the atlas map with .Values, like `dict "atlas" .Values.atlas`
	merged := map[string]any{"atlas": values["atlas"]}

	templateValues, err := list(thisRelease, "values")
	if err != nil {
		return nil, err
	}
	for _, entry := range templateValues {
		switch v := entry.(type) {
		case string:
			abs := templateDir + "/" + v
			if ok, err := isFile(abs); err != nil {
				return nil, err
			} else if !ok {
				return nil, fmt.Errorf("values-loader: release %q: values file not found: %s", release["Name"], abs)
			}
			loaded, err := e.valuesFile(abs, v, redact, skipSecrets, func() (map[string]any, error) {
				p := merge(deepCopy(values), deepCopy(hierarchy))
				p = merge(p, deepCopy(merged))
				for _, iv := range instanceValues {
					if kindIsMap(iv) {
						p = merge(p, iv.(map[string]any))
					}
				}
				p["Release"] = release
				p["Values"] = deepCopy(p)
				return p, nil
			})
			if err != nil {
				return nil, err
			}
			merged = merge(merged, loaded)
		default:
			if kindIsMap(entry) {
				merged = merge(merged, entry.(map[string]any))
			}
		}
	}

	secrets, err := list(thisRelease, "secrets")
	if err != nil {
		return nil, err
	}
	for _, entry := range secrets {
		v, ok := entry.(string)
		if !ok {
			continue
		}
		abs := templateDir + "/" + v
		if ok, err := isFile(abs); err != nil {
			return nil, err
		} else if !ok {
			return nil, fmt.Errorf("values-loader: release %q: secrets file not found: %s", release["Name"], abs)
		}
		loaded, err := e.sopsLoad(abs, redact, skipSecrets)
		if err != nil {
			return nil, err
		}
		merged = merge(merged, loaded)
	}

	merged = merge(merged, hierarchy)

	for _, entry := range instanceValues {
		switch v := entry.(type) {
		case string:
			abs := deploymentDir + "/" + v
			if ok, err := isFile(abs); err != nil {
				return nil, err
			} else if !ok {
				return nil, fmt.Errorf("values-loader: release %q: instance values file not found: %s", release["Name"], abs)
			}
			loaded, err := e.valuesFile(abs, v, redact, skipSecrets, func() (map[string]any, error) {
				p := merge(deepCopy(values), deepCopy(hierarchy))
				p = merge(p, deepCopy(merged))
				p["Release"] = release
				p["Values"] = deepCopy(p)
				return p, nil
			})
			if err != nil {
				return nil, err
			}
			merged = merge(merged, loaded)
		default:
			if kindIsMap(entry) {
				merged = merge(merged, entry.(map[string]any))
			}
		}
	}

	instanceSecrets, err := list(this, "secrets")
	if err != nil {
		return nil, err
	}
	for _, entry := range instanceSecrets {
		v, ok := entry.(string)
		if !ok {
			continue
		}
		abs := deploymentDir + "/" + v
		if ok, err := isFile(abs); err != nil {
			return nil, err
		} else if !ok {
			return nil, fmt.Errorf("values-loader: release %q: instance secrets file not found: %s", release["Name"], abs)
		}
		loaded, err := e.sopsLoad(abs, redact, skipSecrets)
		if err != nil {
			return nil, err
		}
		merged = merge(merged, loaded)
	}
	return merged, nil
}

var _ = isString
