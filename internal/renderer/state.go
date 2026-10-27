// Package renderer renders the states the native engine generates without
// helmfile and without the helm binary: the helm v4 SDK renders each release,
// kustomize (krusty) applies the release patches. It is a drop-in for
// `helmfile template` on these states, and only for these: it reads the
// subset of the helmfile state format the engine writes and refuses any
// other field.
package renderer

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"text/template"

	"github.com/max06/atlas/internal/engine"

	"go.yaml.in/yaml/v3"
)

// Root is the generated root state: the instance states in render order.
type Root struct {
	Dir    string
	States []*State
}

// State is one generated instance state.
type State struct {
	// File is the state path relative to the root directory (helmfile's
	// FilePath of a sub-helmfile).
	File         string
	Env          map[string]any
	Repositories []Repository
	CommonLabels map[string]string
	KubeVersion  string
	// KubeContext is helmDefaults.kubeContext.
	KubeContext string
	Releases    []Release
}

// Repository is a chart repository of a state (helm repo add, or an OCI
// registry prefix).
type Repository struct {
	Name string
	URL  string
	OCI  bool
}

// Release is one release of a state, as the engine writes it.
type Release struct {
	Name                  string
	Namespace             string
	Chart                 string
	Version               string
	Values                []any
	Labels                map[string]string
	SkipSchemaValidation  *bool
	JSONPatches           []any
	StrategicMergePatches []any
	Transformers          []any
	Set, SetString        []SetValue
	KubeContext           string
	// PostRenderer is a helm post-renderer plugin name (atlas-redact).
	PostRenderer     string
	PostRendererArgs []string
}

// SetValue is one entry of set / setString.
type SetValue struct {
	Name, Value, File string
	Values            []any
}

// carrierFile is the engine's release-less carrier state (engine.CarrierFile).
const carrierFile = "helmfile.discovery-map.yaml"

// The documents are decoded into typed structs like helmfile does
// (go.yaml.in/yaml/v3, string fields keep the scalar text: version 1.10
// stays "1.10"), with KnownFields: any field the renderer does not
// implement, and any type mismatch, is an error instead of being ignored.
type rootDoc struct {
	Helmfiles []struct {
		Path string `yaml:"path"`
	} `yaml:"helmfiles"`
}

type envDoc struct {
	Environments map[string]struct {
		Values []map[string]any `yaml:"values"`
	} `yaml:"environments"`
}

type stateDoc struct {
	KubeVersion  string            `yaml:"kubeVersion"`
	CommonLabels map[string]string `yaml:"commonLabels"`
	Repositories []struct {
		Name string `yaml:"name"`
		URL  string `yaml:"url"`
		OCI  bool   `yaml:"oci"`
	} `yaml:"repositories"`
	// helmDefaults that template sees: kubeContext (.Release.KubeContext);
	// timeout and wait only act on sync/apply
	HelmDefaults struct {
		KubeContext string `yaml:"kubeContext"`
		Timeout     int    `yaml:"timeout"`
		Wait        bool   `yaml:"wait"`
	} `yaml:"helmDefaults"`
	// helmfiles: only the release-less carrier state of the redaction maps
	// (redactMaps), which `helmfile build` shows and template renders as
	// nothing
	Helmfiles []struct {
		Path   string `yaml:"path"`
		Values []any  `yaml:"values"`
	} `yaml:"helmfiles"`
	Releases []releaseDoc `yaml:"releases"`
}

type releaseDoc struct {
	Name                  string            `yaml:"name"`
	Namespace             string            `yaml:"namespace"`
	Chart                 string            `yaml:"chart"`
	Version               string            `yaml:"version"`
	Values                []any             `yaml:"values"`
	Labels                map[string]string `yaml:"labels"`
	SkipSchemaValidation  *bool             `yaml:"skipSchemaValidation"`
	JSONPatches           []any             `yaml:"jsonPatches"`
	StrategicMergePatches []any             `yaml:"strategicMergePatches"`
	Transformers          []any             `yaml:"transformers"`
	Set                   []setDoc          `yaml:"set"`
	SetString             []setDoc          `yaml:"setString"`
	PostRenderer          string            `yaml:"postRenderer"`
	PostRendererArgs      []string          `yaml:"postRendererArgs"`
	// needs only orders sync/apply, keyring only verifies: template
	// ignores both
	Needs   []string `yaml:"needs"`
	Keyring string   `yaml:"keyring"`
}

type setDoc struct {
	Name   string `yaml:"name"`
	Value  string `yaml:"value"`
	File   string `yaml:"file"`
	Values []any  `yaml:"values"`
}

// strictDecode decodes one document node into v, refusing unknown fields.
func strictDecode(node *yaml.Node, v any) error {
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}

// documents splits a file into its YAML documents.
func documents(data []byte) ([]*yaml.Node, error) {
	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, &n)
	}
}

// LoadRoot reads a generated root state (one document with helmfiles[].path
// and nothing else) and its instance states.
func LoadRoot(file string) (*Root, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	docs, err := documents(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(docs) != 1 {
		return nil, fmt.Errorf("%s: a generated root has one document, found %d", file, len(docs))
	}
	var root rootDoc
	if err := strictDecode(docs[0], &root); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	dir, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return nil, err
	}
	r := &Root{Dir: dir}
	for _, h := range root.Helmfiles {
		st, err := LoadState(r.Dir, h.Path)
		if err != nil {
			return nil, err
		}
		r.States = append(r.States, st)
	}
	return r, nil
}

// LoadState reads one state: an environments document (the engine writes
// the default environment with inline value maps), then the state document
// (writeState of the engine).
func LoadState(dir, rel string) (*State, error) {
	file := filepath.Join(dir, rel)
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	docs, err := documents(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	st := &State{File: rel, Env: map[string]any{}}
	for _, doc := range docs {
		if hasKey(doc, "environments") {
			var env envDoc
			if err := strictDecode(doc, &env); err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			for name, e := range env.Environments {
				if name != "default" {
					return nil, fmt.Errorf("%s: environment %q: only the default environment is supported", file, name)
				}
				for _, m := range e.Values {
					for k, v := range m {
						st.Env[k] = v
					}
				}
			}
			continue
		}
		var sd stateDoc
		if err := strictDecode(doc, &sd); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if err := st.read(sd); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
	}
	return st, nil
}

func hasKey(doc *yaml.Node, key string) bool {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return true
		}
	}
	return false
}

func (st *State) read(sd stateDoc) error {
	for i, h := range sd.Helmfiles {
		if filepath.Base(h.Path) != carrierFile {
			return fmt.Errorf("helmfiles[%d]: only the carrier state %s is supported", i, carrierFile)
		}
	}
	st.KubeVersion = sd.KubeVersion
	st.KubeContext = sd.HelmDefaults.KubeContext
	st.CommonLabels = sd.CommonLabels
	for _, r := range sd.Repositories {
		st.Repositories = append(st.Repositories, Repository{Name: r.Name, URL: r.URL, OCI: r.OCI})
	}
	for i, rd := range sd.Releases {
		if rd.Name == "" || rd.Chart == "" {
			return fmt.Errorf("releases[%d]: release without name or chart", i)
		}
		r := Release{
			Name: rd.Name, Namespace: rd.Namespace, Chart: rd.Chart, Version: rd.Version,
			Values: rd.Values, Labels: rd.Labels, SkipSchemaValidation: rd.SkipSchemaValidation,
			JSONPatches: rd.JSONPatches, StrategicMergePatches: rd.StrategicMergePatches, Transformers: rd.Transformers,
			PostRenderer: rd.PostRenderer, PostRendererArgs: rd.PostRendererArgs,
		}
		for _, sv := range rd.Set {
			r.Set = append(r.Set, SetValue(sv))
		}
		for _, sv := range rd.SetString {
			r.SetString = append(r.SetString, SetValue(sv))
		}
		st.Releases = append(st.Releases, r)
	}
	return nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// Prepared returns the releases as `helmfile build` shows them, the
// .Release of the values computation: commonLabels copied over the release
// labels and the helmDefaults kubeContext as default, the release template
// expressions rendered (ExecuteTemplates), then the selector labels name,
// namespace and chart (GetReleasesWithLabels; chart = the last path
// segment). namespaceOverride is helmfile's --namespace (the .Namespace of
// the release templates).
func (st *State) Prepared(namespaceOverride string) ([]Release, error) {
	out := make([]Release, len(st.Releases))
	for i, r := range st.Releases {
		labels := map[string]string{}
		for k, v := range r.Labels {
			labels[k] = v
		}
		for k, v := range st.CommonLabels {
			labels[k] = v
		}
		r.Labels = labels
		if r.KubeContext == "" {
			r.KubeContext = st.KubeContext
		}
		r, err := st.executeTemplates(r, namespaceOverride)
		if err != nil {
			return nil, err
		}
		if r.Chart == "" {
			return nil, fmt.Errorf("encountered empty chart while reading release %q", r.Name)
		}
		labels = map[string]string{}
		for k, v := range r.Labels {
			labels[k] = v
		}
		labels["name"] = r.Name
		labels["namespace"] = r.Namespace
		labels["chart"] = r.Chart[strings.LastIndex(r.Chart, "/")+1:]
		r.Labels = labels
		out[i] = r
	}
	return out, nil
}

// executeTemplates is helmfile's release template fixpoint (at most six
// rounds, each with the .Release of the previous one): name, chart,
// namespace, version (trimmed), the label values, the post-renderer
// arguments and the values entries that are paths. Inline value maps stay
// as they are.
func (st *State) executeTemplates(r Release, namespaceOverride string) (Release, error) {
	if !hasTemplate(r) {
		// a field without template expressions renders to itself; helmfile
		// trims the four names in any case
		r.Name, r.Chart = strings.TrimSpace(r.Name), strings.TrimSpace(r.Chart)
		r.Namespace, r.Version = strings.TrimSpace(r.Namespace), strings.TrimSpace(r.Version)
		return r, nil
	}
	t, err := templater()
	if err != nil {
		return r, err
	}
	// every round renders the original release; only the template data
	// (.Release) comes from the previous round
	prev := r
	for it := 0; it < 6; it++ {
		data := map[string]any{
			"Environment": map[string]any{"Name": "default", "KubeContext": "", "Values": st.Env},
			"KubeContext": "",
			"Namespace":   namespaceOverride,
			"Chart":       "",
			"Values":      st.Env,
			"StateValues": st.Env,
			"Release": map[string]any{
				"Name": prev.Name, "Chart": prev.Chart, "Namespace": prev.Namespace,
				"Labels": prev.Labels, "KubeContext": prev.KubeContext, "ChartVersion": prev.Version,
			},
		}
		render := func(field, s string) (string, error) {
			out, err := t.Tpl(s, data)
			if err != nil {
				return "", fmt.Errorf("failed executing template expressions in release \"%s\".%s = \"%s\": %w", r.Name, field, s, err)
			}
			return out, nil
		}
		next := r
		for _, f := range []struct {
			name string
			v    *string
		}{{"name", &next.Name}, {"chart", &next.Chart}, {"namespace", &next.Namespace}, {"version", &next.Version}} {
			out, err := render(f.name, *f.v)
			if err != nil {
				return r, err
			}
			*f.v = strings.TrimSpace(out)
		}
		next.Labels = map[string]string{}
		for k, v := range r.Labels {
			out, err := render("labels."+k, v)
			if err != nil {
				return r, err
			}
			next.Labels[k] = out
		}
		next.PostRendererArgs = nil
		for _, a := range r.PostRendererArgs {
			out, err := render("postRendererArgs", a)
			if err != nil {
				return r, err
			}
			next.PostRendererArgs = append(next.PostRendererArgs, out)
		}
		next.Values = make([]any, len(r.Values))
		for i, v := range r.Values {
			if sv, ok := v.(string); ok {
				out, err := render(fmt.Sprintf("values[%d]", i), sv)
				if err != nil {
					return r, err
				}
				next.Values[i] = out
				continue
			}
			next.Values[i] = v
		}
		if reflect.DeepEqual(prev, next) {
			return next, nil
		}
		prev = next
	}
	return r, fmt.Errorf("failed executing templates in release \"%s\": recursive references can't be resolved", r.Name)
}

func hasTemplate(r Release) bool {
	strs := []string{r.Name, r.Chart, r.Namespace, r.Version}
	strs = append(strs, r.PostRendererArgs...)
	for _, v := range r.Labels {
		strs = append(strs, v)
	}
	for _, v := range r.Values {
		if s, ok := v.(string); ok {
			strs = append(strs, s)
		}
	}
	for _, s := range strs {
		if strings.Contains(s, "{{") {
			return true
		}
	}
	return false
}

var (
	templaterOnce sync.Once
	sharedTpl     *engine.Templater
	templaterErr  error
)

// templater is the engine's template renderer (helmfile's function set),
// created on first use: only states with release templates need it.
func templater() (*engine.Templater, error) {
	templaterOnce.Do(func() { sharedTpl, templaterErr = engine.NewTemplater() })
	return sharedTpl, templaterErr
}

// deploymentOf is atlas.deployment.<key> of the state environment.
func (st *State) deploymentOf(key string) string {
	atlas, _ := st.Env["atlas"].(map[string]any)
	d, _ := atlas["deployment"].(map[string]any)
	return str(d[key])
}

// DefaultOutputDirTemplate is helmfile's output dir template when none is
// given.
const DefaultOutputDirTemplate = "{{ .OutputDir }}/{{ .State.BaseName }}-{{ .State.AbsPathSHA1 }}-{{ .Release.Name}}"

// outputDir is helmfile's GenerateOutputDir: the output dir template
// rendered with the state (helmfile processes a sub-helmfile from its own
// directory, so FilePath is the base name), the release and the
// environment.
func (st *State) outputDir(rootDir, base, tmpl string, r Release) (string, error) {
	if tmpl == "" {
		tmpl = DefaultOutputDirTemplate
	}
	filePath := filepath.Base(st.File)
	abs := filepath.Join(rootDir, st.File)
	sum := sha1.Sum([]byte(abs))
	t, err := template.New("output-dir").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing output-dir template")
	}
	data := map[string]any{
		"OutputDir": base,
		"State": map[string]any{
			"BaseName":    strings.TrimSuffix(filePath, filepath.Ext(filePath)),
			"Path":        filePath,
			"AbsPath":     abs,
			"AbsPathSHA1": hex.EncodeToString(sum[:])[:8],
		},
		"Release": map[string]any{
			"Name": r.Name, "Namespace": r.Namespace, "Chart": r.Chart, "Version": r.Version, "Labels": r.Labels,
		},
		"Environment": map[string]any{"Name": "default", "KubeContext": "", "Values": st.Env},
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("executing output-dir template: %w", err)
	}
	return b.String(), nil
}

// Selector is one helmfile --selector: label conditions joined by AND.
type Selector []condition

type condition struct {
	key, value string
	negate     bool
}

// ParseSelector reads key=value,key!=value with helmfile's label syntax.
func ParseSelector(s string) (Selector, error) {
	var sel Selector
	for _, part := range strings.Split(s, ",") {
		switch {
		case reLabelMismatch.MatchString(part):
			k, v, _ := strings.Cut(part, "!=")
			sel = append(sel, condition{key: k, value: v, negate: true})
		case reLabelMatch.MatchString(part):
			k, v, _ := strings.Cut(part, "=")
			sel = append(sel, condition{key: k, value: v})
		default:
			return nil, fmt.Errorf("malformed label: %s. Expected label in form k=v or k!=v", part)
		}
	}
	return sel, nil
}

// helmfile's label filter syntax (release_filters.go)
var (
	reLabelMismatch = regexp.MustCompile(`^[a-zA-Z0-9_\.\/\+-]+!=[a-zA-Z0-9_\.\/\+-]+$`)
	reLabelMatch    = regexp.MustCompile(`^[a-zA-Z0-9_\.\/\+-]+=[a-zA-Z0-9_\.\/\+-]+$`)
)

// matches is helmfile's LabelFilter on the prepared labels.
func (s Selector) matches(labels map[string]string) bool {
	for _, c := range s {
		v, ok := labels[c.key]
		if c.negate {
			if ok && v == c.value {
				return false
			}
		} else if !ok || v != c.value {
			return false
		}
	}
	return true
}
