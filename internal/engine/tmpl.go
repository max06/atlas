package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/helmfile/helmfile/pkg/filesystem"
	"github.com/helmfile/helmfile/pkg/tmpl"

	"github.com/max06/atlas/templates"
)

// Templater renders text like helmfile's `tpl` inside the atlas templates:
// sprig plus helmfile's functions, missingkey=error, the atlas templates
// directory as base path (its _*.tpl helpers in scope, relative paths and
// exec there).
type Templater struct {
	ctx *tmpl.Context
	dir string
}

var (
	baseOnce sync.Once
	baseDir  string
	baseErr  error
)

// templatesBase extracts the embedded atlas templates into a directory, once
// per process.
func templatesBase() (string, error) {
	baseOnce.Do(func() {
		dir, err := os.MkdirTemp("", "atlas-templates-")
		if err != nil {
			baseErr = err
			return
		}
		baseErr = fs.WalkDir(templates.Files, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := templates.Files.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, p), data, 0o644)
		})
		baseDir = dir
	})
	return baseDir, baseErr
}

// NewTemplater builds the template context.
func NewTemplater() (*Templater, error) {
	dir, err := templatesBase()
	if err != nil {
		return nil, err
	}
	c := &tmpl.Context{}
	c.SetBasePath(dir)
	c.SetFileSystem(filesystem.DefaultFileSystem())
	return &Templater{ctx: c, dir: dir}, nil
}

// Tpl is `tpl text data`.
func (t *Templater) Tpl(text string, data any) (string, error) {
	return t.ctx.Tpl(text, data)
}

// The sprig and helmfile functions the templates call, with their exact
// semantics (mergeOverwrite: mergo with override, an empty source value
// does not override; deepCopy: copystructure).
var (
	funcs            = sprig.TxtFuncMap()
	mergeOverwrite   = funcs["mergeOverwrite"].(func(map[string]any, ...map[string]any) any)
	deepCopyFn       = funcs["deepCopy"].(func(any) any)
	omit             = funcs["omit"].(func(map[string]any, ...string) map[string]any)
	uniqFn           = funcs["uniq"].(func(any) []any)
	sortAlpha        = funcs["sortAlpha"].(func(any) []string)
	toStringFn       = funcs["toString"].(func(any) string)
	helmfileGet      = (&tmpl.Context{}).CreateFuncMap()["get"].(func(string, ...any) (any, error))
	fetchSecretValue = (&tmpl.Context{}).CreateFuncMap()["fetchSecretValue"].(func(string) (string, error))
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
	return helmfileGet(key, def, untyped(obj))
}

// untyped turns a nil map back into the nil a template sees for a YAML
// document without content: helmfile's get fails on it, it does not
// return the default.
func untyped(v any) any {
	if m, ok := v.(map[string]any); ok && m == nil {
		return nil
	}
	return v
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

// list is `range $x := (obj | get "key" list)`.
func list(obj any, key string) ([]any, error) {
	v, err := get(obj, key, []any{})
	if err != nil {
		return nil, err
	}
	return rangeValues(v, key)
}

// rangeValues is what `range` iterates: the elements of a list, the values
// of a map in key order, nothing for nil.
func rangeValues(v any, what string) ([]any, error) {
	switch l := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return l, nil
	case map[string]any:
		out := make([]any, 0, len(l))
		for _, k := range sortedKeys(l) {
			out = append(out, l[k])
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: range can't iterate over %v", what, v)
}

// str is `obj | get "key" def | toString`.
func str(obj any, key, def string) (string, error) {
	v, err := get(obj, key, def)
	if err != nil {
		return "", err
	}
	return toStringFn(v), nil
}

// rawString is a value a template hands to a string parameter (hasPrefix):
// a string, anything else is an error.
func rawString(v any, what string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: invalid value; expected string, got %T", what, v)
	}
	return s, nil
}

// truthy is the template's truth of a value (`if`, `and`, `or`).
func truthy(v any) bool {
	t, _ := template.IsTrue(v)
	return t
}

// synthRelease is the placeholder .Release of state-build renders.
func synthRelease() map[string]any { return map[string]any{"Name": "", "Namespace": ""} }
