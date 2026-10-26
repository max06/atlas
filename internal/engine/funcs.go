package engine

// The template engine of helmfile's pkg/tmpl, ported (helmfile v1.8.1,
// MIT License, Copyright (c) 2017 rob boll and the helmfile contributors).
//
// The native engine renders the atlas templates and the consumer's gotmpl
// files exactly like helmfile: sprig, then helmfile's functions over it
// (helmfile's `get`; sprig's get as sprigGet), missingkey=error, every
// _*.tpl of the base path as named templates, `include` with a recursion
// limit. The port differs in one place: fetchSecretValue reads ref+sops://
// directly with the sops library. helmfile goes through vals, which links
// the SDK of every secret provider (about 220 MB of binary); ATLAS and its
// consumers use ref+sops:// only.

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/getsops/sops/v3/decrypt"
	"github.com/helmfile/helmfile/pkg/maputil"
	hfyaml "github.com/helmfile/helmfile/pkg/yaml"
)

const recursionMaxNums = 1000

// tmplContext is helmfile's tmpl.Context: the base path of relative file
// functions, exec and the helper templates.
type tmplContext struct {
	basePath string
}

// insecure is helmfile's switch HELMFILE_DISABLE_INSECURE_FEATURES (or
// ..._TEMPLATE_FUNCTIONS): exec and the file reads fail.
func insecureDisabled() bool {
	for _, name := range []string{"HELMFILE_DISABLE_INSECURE_FEATURES", "HELMFILE_DISABLE_INSECURE_TEMPLATE_FUNCTIONS"} {
		if v := strings.ToLower(os.Getenv(name)); v == "1" || v == "true" || v == "t" {
			return true
		}
	}
	return false
}

var errInsecure = errors.New("HELMFILE_DISABLE_INSECURE_FEATURES or HELMFILE_DISABLE_INSECURE_TEMPLATE_FUNCTIONS is active, insecure function calls are disabled")

func (c *tmplContext) path(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(c.basePath, name)
}

func (c *tmplContext) funcMap() template.FuncMap {
	funcMap := sprig.TxtFuncMap()
	sprigGet := funcMap["get"]
	helmfileFuncs := template.FuncMap{
		"envExec":          c.envExec,
		"exec":             c.exec,
		"isFile":           c.isFile,
		"isDir":            c.isDir,
		"readFile":         c.readFile,
		"readDir":          c.readDir,
		"readDirEntries":   c.readDirEntries,
		"toYaml":           tmplToYaml,
		"fromYaml":         tmplFromYaml,
		"setValueAtPath":   setValueAtPath,
		"requiredEnv":      requiredEnv,
		"get":              tmplGet,
		"getOrNil":         getOrNil,
		"tpl":              c.tpl,
		"required":         required,
		"fetchSecretValue": fetchSecretValue,
		"expandSecretRefs": expandSecretRefs,
	}
	for k, v := range helmfileFuncs {
		funcMap[k] = v
	}
	funcMap["sprigGet"] = sprigGet
	return funcMap
}

func (c *tmplContext) newTemplate() (*template.Template, error) {
	funcMap := c.funcMap()
	t := template.New("stringTemplate").Option("missingkey=error")
	included := map[string]int{}
	funcMap["include"] = func(name string, data any) (string, error) {
		var buf strings.Builder
		if included[name] > recursionMaxNums {
			return "", fmt.Errorf("rendering template has a nested reference name: %s: unable to execute template", name)
		}
		included[name]++
		err := t.ExecuteTemplate(&buf, name, data)
		included[name]--
		return buf.String(), err
	}
	t.Funcs(funcMap)
	helpers, err := filepath.Glob(filepath.Join(c.basePath, "_*.tpl"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob helper templates: %v", err)
	}
	for _, file := range helpers {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read helper template %s: %v", file, err)
		}
		if t, err = t.Parse(string(content)); err != nil {
			return nil, fmt.Errorf("failed to parse helper template %s: %v", file, err)
		}
	}
	return t, nil
}

// render is RenderTemplateToBuffer.
func (c *tmplContext) render(s string, data any) (string, error) {
	t, err := c.newTemplate()
	if err != nil {
		return "", err
	}
	if t, err = t.Parse(s); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, data)
	return buf.String(), err
}

func (c *tmplContext) tpl(text string, data any) (string, error) { return c.render(text, data) }

func (c *tmplContext) envExec(envs map[string]any, command string, args []any, inputs ...string) (string, error) {
	if insecureDisabled() {
		return "", errInsecure
	}
	strArgs := make([]string, len(args))
	for i, a := range args {
		s, ok := a.(string)
		if !ok {
			return "", fmt.Errorf("unexpected type of arg \"%s\" in args %v at index %d", reflect.TypeOf(a), args, i)
		}
		strArgs[i] = s
	}
	cmd := exec.Command(command, strArgs...)
	cmd.Dir = c.basePath
	if envs != nil {
		cmd.Env = os.Environ()
		for k, v := range envs {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("unexpected type of env \"%s\" in envs %v at index %s", reflect.TypeOf(v), envs, k)
			}
			cmd.Env = append(cmd.Env, k+"="+s)
		}
	}
	if len(inputs) > 0 && inputs[0] != "" {
		cmd.Stdin = strings.NewReader(inputs[0])
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (c *tmplContext) exec(command string, args []any, inputs ...string) (string, error) {
	return c.envExec(nil, command, args, inputs...)
}

func (c *tmplContext) isFile(name string) (bool, error) { return isFile(c.path(name)) }
func (c *tmplContext) isDir(name string) (bool, error)  { return isDir(c.path(name)) }

func (c *tmplContext) readFile(name string) (string, error) {
	if insecureDisabled() {
		return "", errInsecure
	}
	data, err := os.ReadFile(c.path(name))
	return string(data), err
}

func (c *tmplContext) readDir(name string) ([]string, error) {
	if insecureDisabled() {
		return nil, errInsecure
	}
	entries, err := os.ReadDir(c.path(name))
	if err != nil {
		return nil, fmt.Errorf("ReadDir %q: %w", c.path(name), err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() {
			paths = append(paths, filepath.Join(name, e.Name()))
		}
	}
	return paths, nil
}

func (c *tmplContext) readDirEntries(name string) ([]fs.DirEntry, error) {
	if insecureDisabled() {
		return nil, errInsecure
	}
	entries, err := os.ReadDir(c.path(name))
	if err != nil {
		return nil, fmt.Errorf("ReadDirEntries %q: %w", c.path(name), err)
	}
	return entries, nil
}

func tmplToYaml(v any) (string, error) {
	data, err := hfyaml.Marshal(v)
	return string(data), err
}

func tmplFromYaml(str string) (any, error) {
	var m any
	if err := hfyaml.Unmarshal([]byte(str), &m); err != nil {
		return nil, fmt.Errorf("%s, offending yaml: %s", err, str)
	}
	m, err := maputil.RecursivelyStringifyMapKey(m)
	if err != nil {
		return nil, fmt.Errorf("%s, offending yaml: %s", err, str)
	}
	return m, nil
}

func setValueAtPath(path string, value any, values map[string]any) (map[string]any, error) {
	var current any = values
	components := strings.Split(path, ".")
	pathToMap, key := components[:len(components)-1], components[len(components)-1]
	for _, k := range pathToMap {
		var elem any
		switch typed := current.(type) {
		case map[string]any:
			v, ok := typed[k]
			if !ok {
				return nil, fmt.Errorf("failed to set value at path \"%s\": value for key \"%s\" does not exist", path, k)
			}
			elem = v
		case map[any]any:
			v, ok := typed[k]
			if !ok {
				return nil, fmt.Errorf("failed to set value at path \"%s\": value for key \"%s\" does not exist", path, k)
			}
			elem = v
		default:
			return nil, fmt.Errorf("failed to set value at path \"%s\": value for key \"%s\" was not a map", path, k)
		}
		switch elem.(type) {
		case map[string]any, map[any]any:
			current = elem
		default:
			return nil, fmt.Errorf("failed to set value at path \"%s\": value for key \"%s\" was not a map", path, k)
		}
	}
	switch typed := current.(type) {
	case map[string]any:
		typed[key] = value
	case map[any]any:
		typed[key] = value
	default:
		return nil, fmt.Errorf("failed to set value at path \"%s\": value for key \"%s\" was not a map", path, key)
	}
	return values, nil
}

func requiredEnv(name string) (string, error) {
	if v, ok := os.LookupEnv(name); ok && len(v) > 0 {
		return v, nil
	}
	return "", fmt.Errorf("required env var `%s` is not set", name)
}

func required(warn string, val any) (any, error) {
	if val == nil {
		return nil, errors.New(warn)
	}
	if s, ok := val.(string); ok && s == "" {
		return nil, errors.New(warn)
	}
	return val, nil
}

type noValueError struct{ msg string }

func (e *noValueError) Error() string { return e.msg }

// tmplGet is helmfile's get: a dotted path, an optional default.
func tmplGet(path string, varArgs ...any) (any, error) {
	var defSet bool
	var def, obj any
	switch len(varArgs) {
	case 1:
		obj = varArgs[0]
	case 2:
		defSet, def, obj = true, varArgs[0], varArgs[1]
	default:
		return nil, fmt.Errorf("unexpected number of args passed to the template function get(path, [def, ]obj): expected 1 or 2, got %d, args was %v", len(varArgs), varArgs)
	}
	if path == "" {
		return obj, nil
	}
	keys := strings.Split(path, ".")
	var v any
	var ok bool
	if p, isPtr := obj.(*map[string]any); isPtr {
		obj = *p
	}
	missing := func(m any) (any, error) {
		if defSet {
			return def, nil
		}
		return nil, &noValueError{fmt.Sprintf("no value exist for key \"%s\" in %v", keys[0], m)}
	}
	switch typed := obj.(type) {
	case map[string]any:
		if v, ok = typed[keys[0]]; !ok {
			return missing(typed)
		}
	case map[string]string:
		if v, ok = typed[keys[0]]; !ok {
			return missing(typed)
		}
		return v, nil
	case map[any]any:
		if v, ok = typed[keys[0]]; !ok {
			return missing(typed)
		}
	default:
		s := reflect.ValueOf(typed)
		if s.Kind() != reflect.Struct {
			return nil, &noValueError{fmt.Sprintf("unexpected type(%v) of value for key \"%s\": it must be either map[string]any or any struct", reflect.TypeOf(obj), keys[0])}
		} else if s.NumField() < 1 {
			return nil, &noValueError{fmt.Sprintf("no accessible struct fields for key \"%s\"", keys[0])}
		}
		f := s.FieldByName(keys[0])
		if !f.IsValid() {
			if defSet {
				return def, nil
			}
			return nil, &noValueError{fmt.Sprintf("no field named \"%s\" exist in %v", keys[0], typed)}
		}
		v = f.Interface()
	}
	if defSet {
		return tmplGet(strings.Join(keys[1:], "."), def, v)
	}
	return tmplGet(strings.Join(keys[1:], "."), v)
}

func getOrNil(path string, o any) (any, error) {
	v, err := tmplGet(path, o)
	var nv *noValueError
	if errors.As(err, &nv) {
		return nil, nil
	}
	return v, err
}

// fetchSecretValue resolves a ref+sops:// reference (vals' syntax: the
// file path, ?format=, no #fragment for the whole file) to the decrypted
// text, as vals' sops provider does (sops decrypt, MAC check, plain emit).
func fetchSecretValue(ref string) (string, error) {
	rest, ok := strings.CutPrefix(ref, "ref+sops://")
	if !ok {
		return "", fmt.Errorf("fetchSecretValue %q: the native engine resolves ref+sops:// only", ref)
	}
	file, query, _ := strings.Cut(rest, "?")
	if strings.Contains(file, "#") || strings.Contains(query, "#") {
		return "", fmt.Errorf("fetchSecretValue %q: #fragments are not supported by the native engine", ref)
	}
	params, err := url.ParseQuery(query)
	if err != nil {
		return "", fmt.Errorf("fetchSecretValue %q: %w", ref, err)
	}
	format := params.Get("format")
	if format == "" {
		format = strings.TrimPrefix(filepath.Ext(file), ".")
	}
	data, err := decrypt.File(file, format)
	if err != nil {
		return "", fmt.Errorf("fetchSecretValue %q: %w", ref, err)
	}
	return string(data), nil
}

// expandSecretRefs is helmfile's: every ref+ string of a map resolved.
func expandSecretRefs(values map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range values {
		resolved, err := expandSecretValue(v)
		if err != nil {
			return nil, err
		}
		out[k] = resolved
	}
	return out, nil
}

func expandSecretValue(v any) (any, error) {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "ref+") {
			return fetchSecretValue(x)
		}
		return x, nil
	case map[string]any:
		return expandSecretRefs(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := expandSecretValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}
