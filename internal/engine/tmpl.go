package engine

import (
	"fmt"
	"io/fs"
	"reflect"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/helmfile/helmfile/pkg/filesystem"
	"github.com/helmfile/helmfile/pkg/tmpl"

	"github.com/max06/atlas/templates"
)

// helperBase is the base path of the template context: helmfile renders
// atlas files with the atlas templates directory as base path, so its
// _*.tpl helpers are in scope. The native engine serves them from the
// binary under this virtual directory.
const helperBase = "/atlas-native-helpers"

// Templater renders text like helmfile's `tpl` inside the atlas templates:
// sprig plus helmfile's functions, missingkey=error, the atlas helpers.
type Templater struct {
	ctx *tmpl.Context
}

// NewTemplater builds the template context.
func NewTemplater() *Templater {
	fsys := filesystem.DefaultFileSystem()
	readFile := fsys.ReadFile
	fsys.Glob = func(pattern string) ([]string, error) {
		if pattern != helperBase+"/_*.tpl" {
			return nil, fmt.Errorf("native engine: unexpected glob %q", pattern)
		}
		names, err := fs.Glob(templates.Helpers, "_*.tpl")
		for i, n := range names {
			names[i] = helperBase + "/" + n
		}
		return names, err
	}
	fsys.ReadFile = func(name string) ([]byte, error) {
		if rest, ok := strings.CutPrefix(name, helperBase+"/"); ok {
			return templates.Helpers.ReadFile(rest)
		}
		return readFile(name)
	}
	c := &tmpl.Context{}
	c.SetBasePath(helperBase)
	c.SetFileSystem(fsys)
	return &Templater{ctx: c}
}

// Tpl is `tpl text data`.
func (t *Templater) Tpl(text string, data any) (string, error) {
	return t.ctx.Tpl(text, data)
}

// The sprig and helmfile functions the templates call, with their exact
// semantics (mergeOverwrite: mergo with override, an empty source value
// does not override; deepCopy: copystructure).
var (
	funcs          = sprig.TxtFuncMap()
	mergeOverwrite = funcs["mergeOverwrite"].(func(map[string]any, ...map[string]any) any)
	deepCopyFn     = funcs["deepCopy"].(func(any) any)
	omit           = funcs["omit"].(func(map[string]any, ...string) map[string]any)
	uniqFn         = funcs["uniq"].(func(any) []any)
	sortAlpha      = funcs["sortAlpha"].(func(any) []string)
	toStringFn     = funcs["toString"].(func(any) string)
	helmfileGet    = (&tmpl.Context{}).CreateFuncMap()["get"].(func(string, ...any) (any, error))
)

// merge is `$dst = mergeOverwrite $dst $src`: it mutates dst.
func merge(dst map[string]any, srcs ...map[string]any) map[string]any {
	return mergeOverwrite(dst, srcs...).(map[string]any)
}

func deepCopy(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return deepCopyFn(v).(map[string]any)
}

// get is helmfile's `get path default obj`.
func get(obj any, key string, def any) (any, error) {
	return helmfileGet(key, def, obj)
}

// fromYaml is helmfile's fromYaml. A template passes its result to a map
// parameter: nil stays nil, anything else that is not a map is an error.
func fromYamlMap(s, what string) (map[string]any, error) {
	v, err := tmpl.FromYaml(s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return asMap(v, what)
}

func asMap(v any, what string) (map[string]any, error) {
	switch m := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return m, nil
	}
	return nil, fmt.Errorf("%s: wrong type for value; expected map[string]interface {}; got %T", what, v)
}

// roundTrip is `include ... | fromYaml` of a template that ends with
// `{{ $x | toYaml }}`: the value as the caller sees it.
func roundTrip(v map[string]any, what string) (map[string]any, error) {
	s, err := tmpl.ToYaml(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	m, err := fromYamlMap("\n"+s, what)
	if m == nil && err == nil {
		m = map[string]any{}
	}
	return m, err
}

// kindIsMap is `kindIs "map"`.
func kindIsMap(v any) bool { return v != nil && reflect.ValueOf(v).Kind() == reflect.Map }

// list is `x | get "key" list` for a list value.
func list(obj any, key string) ([]any, error) {
	v, err := get(obj, key, []any{})
	if err != nil {
		return nil, err
	}
	switch l := v.(type) {
	case []any:
		return l, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("%s: expected a list, got %T", key, v)
}

func str(obj any, key, def string) (string, error) {
	v, err := get(obj, key, def)
	if err != nil {
		return "", err
	}
	return toStringFn(v), nil
}

// synthRelease is the placeholder .Release of state-build renders.
func synthRelease() map[string]any { return map[string]any{"Name": "", "Namespace": ""} }
