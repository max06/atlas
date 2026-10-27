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
	"strings"
	"text/template"

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

var (
	stateKeys   = set("commonLabels", "kubeVersion", "releases", "repositories", "helmDefaults")
	releaseKeys = set("name", "namespace", "chart", "version", "values", "labels", "skipSchemaValidation",
		"jsonPatches", "strategicMergePatches", "transformers", "set", "setString",
		"postRenderer", "postRendererArgs",
		// needs only orders sync/apply, keyring only verifies: template ignores both
		"needs", "keyring")
	// helmDefaults that template sees: kubeContext (.Release.KubeContext);
	// timeout and wait only act on sync/apply
	helmDefaultsKeys = set("kubeContext", "timeout", "wait")
	setValueKeys     = set("name", "value", "file", "values")
	repositoryKeys   = set("name", "url", "oci")
)

func set(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// LoadRoot reads a generated root state (helmfile.yaml with helmfiles[].path)
// and its instance states.
func LoadRoot(file string) (*Root, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var root struct {
		Helmfiles []struct {
			Path string `yaml:"path"`
		} `yaml:"helmfiles"`
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	r := &Root{Dir: filepath.Dir(file)}
	for _, h := range root.Helmfiles {
		st, err := LoadState(r.Dir, h.Path)
		if err != nil {
			return nil, err
		}
		r.States = append(r.States, st)
	}
	return r, nil
}

// LoadState reads one state: a document with the environments, then the
// document with the releases (writeState of the engine).
func LoadState(dir, rel string) (*State, error) {
	file := filepath.Join(dir, rel)
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(data, []byte("{{")) {
		// helmfile would render release templates; the engine never writes any
		return nil, fmt.Errorf("%s: template expressions in a generated state are not supported", file)
	}
	st := &State{File: rel, Env: map[string]any{}}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if env, ok := doc["environments"]; ok {
			if err := st.readEnvironments(env); err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			continue
		}
		if err := st.read(doc); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
	}
	return st, nil
}

// readEnvironments takes the values of the default environment (maps only:
// the engine writes the atlas object inline).
func (st *State) readEnvironments(env any) error {
	envs, _ := env.(map[string]any)
	def, _ := envs["default"].(map[string]any)
	list, _ := def["values"].([]any)
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("environments.default.values: only inline maps are supported")
		}
		for k, val := range m {
			st.Env[k] = val
		}
	}
	return nil
}

func (st *State) read(doc map[string]any) error {
	for k := range doc {
		if !stateKeys[k] {
			return fmt.Errorf("unsupported state field %q", k)
		}
	}
	if v, ok := doc["kubeVersion"]; ok {
		st.KubeVersion = fmt.Sprint(v)
	}
	if hd, ok := doc["helmDefaults"].(map[string]any); ok {
		for k := range hd {
			if !helmDefaultsKeys[k] {
				return fmt.Errorf("unsupported helmDefaults field %q", k)
			}
		}
		st.KubeContext = str(hd["kubeContext"])
	}
	labels, err := stringMap(doc["commonLabels"], "commonLabels")
	if err != nil {
		return err
	}
	st.CommonLabels = labels
	repos, _ := doc["repositories"].([]any)
	for i, r := range repos {
		m, ok := r.(map[string]any)
		if !ok {
			return fmt.Errorf("repositories[%d]: not a map", i)
		}
		for k := range m {
			if !repositoryKeys[k] {
				return fmt.Errorf("repositories[%d]: unsupported field %q", i, k)
			}
		}
		st.Repositories = append(st.Repositories, Repository{Name: fmt.Sprint(m["name"]), URL: fmt.Sprint(m["url"]), OCI: m["oci"] == true})
	}
	releases, _ := doc["releases"].([]any)
	for i, r := range releases {
		m, ok := r.(map[string]any)
		if !ok {
			return fmt.Errorf("releases[%d]: not a map", i)
		}
		rel, err := readRelease(m)
		if err != nil {
			return fmt.Errorf("releases[%d]: %w", i, err)
		}
		st.Releases = append(st.Releases, rel)
	}
	return nil
}

func readRelease(m map[string]any) (Release, error) {
	for k := range m {
		if !releaseKeys[k] {
			return Release{}, fmt.Errorf("unsupported release field %q", k)
		}
	}
	r := Release{Name: str(m["name"]), Namespace: str(m["namespace"]), Chart: str(m["chart"]), Version: str(m["version"])}
	if r.Name == "" || r.Chart == "" {
		return r, fmt.Errorf("release without name or chart")
	}
	r.Values, _ = m["values"].([]any)
	r.JSONPatches, _ = m["jsonPatches"].([]any)
	r.StrategicMergePatches, _ = m["strategicMergePatches"].([]any)
	r.Transformers, _ = m["transformers"].([]any)
	labels, err := stringMap(m["labels"], "labels")
	if err != nil {
		return r, err
	}
	r.Labels = labels
	if v, ok := m["skipSchemaValidation"].(bool); ok {
		r.SkipSchemaValidation = &v
	}
	r.PostRenderer = str(m["postRenderer"])
	if args, ok := m["postRendererArgs"].([]any); ok {
		for _, a := range args {
			r.PostRendererArgs = append(r.PostRendererArgs, str(a))
		}
	}
	for _, f := range []struct {
		key string
		dst *[]SetValue
	}{{"set", &r.Set}, {"setString", &r.SetString}} {
		list, _ := m[f.key].([]any)
		for i, e := range list {
			sm, ok := e.(map[string]any)
			if !ok {
				return r, fmt.Errorf("%s[%d]: not a map", f.key, i)
			}
			for k := range sm {
				if !setValueKeys[k] {
					return r, fmt.Errorf("%s[%d]: unsupported field %q", f.key, i, k)
				}
			}
			sv := SetValue{Name: str(sm["name"]), Value: str(sm["value"]), File: str(sm["file"])}
			sv.Values, _ = sm["values"].([]any)
			*f.dst = append(*f.dst, sv)
		}
	}
	return r, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func stringMap(v any, what string) (map[string]string, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: not a map", what)
	}
	out := map[string]string{}
	for k, val := range m {
		out[k] = fmt.Sprint(val)
	}
	return out, nil
}

// Prepared returns the releases as `helmfile build` shows them, the
// .Release of the values computation: commonLabels copied over the release
// labels (ExecuteTemplates), then the selector labels name, namespace and
// chart (GetReleasesWithLabels; chart = the last path segment). The engine
// writes no release templates, so the template fixpoint is the identity.
func (st *State) Prepared() []Release {
	out := make([]Release, len(st.Releases))
	for i, r := range st.Releases {
		labels := map[string]string{}
		for k, v := range r.Labels {
			labels[k] = v
		}
		for k, v := range st.CommonLabels {
			labels[k] = v
		}
		labels["name"] = r.Name
		labels["namespace"] = r.Namespace
		labels["chart"] = r.Chart[strings.LastIndex(r.Chart, "/")+1:]
		r.Labels = labels
		if r.KubeContext == "" {
			r.KubeContext = st.KubeContext
		}
		out[i] = r
	}
	return out
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

// ParseSelector reads key=value,key!=value.
func ParseSelector(s string) (Selector, error) {
	var sel Selector
	for _, part := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(part, "!="); ok {
			sel = append(sel, condition{key: k, value: v, negate: true})
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("malformed label: %s. Expected label in form k=v or k!=v", part)
		}
		sel = append(sel, condition{key: k, value: v})
	}
	return sel, nil
}

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
