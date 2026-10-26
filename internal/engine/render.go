package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateOptions control a generated render.
type GenerateOptions struct {
	// NamespaceOverride is helmfile's --namespace: it becomes the
	// .Release.Namespace of every values computation as well.
	NamespaceOverride string
	// Prepare returns the releases of the generated states as helmfile
	// prepares them (release templates rendered, commonLabels, defaults),
	// keyed by state file name: the .Release of each values computation.
	// nil: .Release from the raw state fields.
	Prepare Preparer
}

// PreparedRelease is one release after helmfile's ExecuteTemplates.
type PreparedRelease struct {
	Name, Namespace, Chart, Version, KubeContext string
	Labels                                       map[string]string
}

// Preparer loads the generated root state (helmfile build).
type Preparer func(root string) (map[string][]PreparedRelease, error)

// Generate writes the native render of the selected pairs into dir: one
// state file per app instance (states/NNNN.yaml, in the template engine's
// order), the values of each release (values/NNNN/<index>.yaml, mode 0600:
// they may hold decrypted secrets) and the root state helmfile.yaml that
// lists the instance states. helmfile renders it with no ATLAS template
// left.
//
// Two passes: the states are written with an empty placeholder for the
// values, Prepare tells the .Release of each release, then the values are
// computed and the states written again.
func (e *Engine) Generate(dir string, f Filter, o GenerateOptions) (string, error) {
	pairs, err := Discover(e.Layout, f)
	if err != nil {
		return "", err
	}
	for _, sub := range []string{"states", "values"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return "", err
		}
	}
	if err := writeYAML(filepath.Join(dir, "states", CarrierFile), map[string]any{"releases": []any{}}); err != nil {
		return "", err
	}
	var instances []*Instance
	for _, p := range pairs {
		list, err := e.Instances(p)
		if err != nil {
			return "", err
		}
		instances = append(instances, list...)
	}
	names := make([]string, len(instances))
	states := []any{}
	for i, inst := range instances {
		names[i] = fmt.Sprintf("%04d.yaml", i+1)
		for _, release := range inst.Releases {
			list := release["values"].([]any)
			list[len(list)-1] = map[string]any{} // placeholder until the values exist
		}
		if err := writeState(filepath.Join(dir, "states", names[i]), inst.State); err != nil {
			return "", err
		}
		states = append(states, map[string]any{"path": "states/" + names[i]})
	}
	root := filepath.Join(dir, "helmfile.yaml")
	if err := writeYAML(root, map[string]any{"helmfiles": states}); err != nil {
		return "", err
	}
	var prepared map[string][]PreparedRelease
	if o.Prepare != nil && len(instances) > 0 {
		if prepared, err = o.Prepare(root); err != nil {
			return "", err
		}
	}
	for i, inst := range instances {
		var releases []PreparedRelease
		if prepared != nil {
			releases = prepared[names[i]]
			if len(releases) != len(inst.Releases) {
				return "", fmt.Errorf("native engine: helmfile reports %d releases for state %s, the engine wrote %d", len(releases), names[i], len(inst.Releases))
			}
		}
		for j, release := range inst.Releases {
			rel := ReleaseContext(release, o.NamespaceOverride)
			if prepared != nil {
				rel = preparedContext(releases[j], o.NamespaceOverride)
			}
			vals, err := e.Values(deepCopy(inst.Values), rel, false)
			if err != nil {
				return "", fmt.Errorf("%s/%s release %s: %w", inst.Pair.Cluster, inst.Pair.DeploymentName, rel["Name"], err)
			}
			file := filepath.Join(dir, "values", strings.TrimSuffix(names[i], ".yaml"), fmt.Sprintf("%d.yaml", j))
			if err := writeYAMLMode(file, vals, 0o600); err != nil {
				return "", err
			}
			list := release["values"].([]any)
			list[len(list)-1] = file
		}
		if err := writeState(filepath.Join(dir, "states", names[i]), inst.State); err != nil {
			return "", err
		}
	}
	return root, nil
}

// preparedContext is .Release of a prepared release.
func preparedContext(r PreparedRelease, namespaceOverride string) map[string]any {
	ns := r.Namespace
	if namespaceOverride != "" {
		ns = namespaceOverride
	}
	labels := map[string]string{}
	for k, v := range r.Labels {
		labels[k] = v
	}
	return map[string]any{"Name": r.Name, "Namespace": ns, "Labels": labels, "Chart": r.Chart, "ChartVersion": r.Version, "KubeContext": r.KubeContext}
}

// writeState writes a state document as helmfile v1 wants a plain YAML
// state: the environments in a part of their own, then the rest.
func writeState(file string, state map[string]any) error {
	rest := map[string]any{}
	for k, v := range state {
		if k != "environments" {
			rest[k] = v
		}
	}
	env, err := tmplToYaml(map[string]any{"environments": state["environments"]})
	if err != nil {
		return err
	}
	body, err := tmplToYaml(rest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(env+"---\n"+body), 0o644)
}

func writeYAML(file string, v any) error { return writeYAMLMode(file, v, 0o644) }

func writeYAMLMode(file string, v any, mode os.FileMode) error {
	s, err := tmplToYaml(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(s), mode)
}

// GenerateDiscoveryMap writes the discovery map mode of the entry into dir:
// a root state whose only sub-state is a release-less carrier holding the
// map as its values (`helmfile build` prints it as renderedvalues).
func (e *Engine) GenerateDiscoveryMap(dir string, f Filter) (string, error) {
	m, err := DiscoveryMap(e.Layout, f, e.switches())
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return "", err
	}
	// the template map always carries charts, an empty list too
	for _, p := range values["pairs"].([]any) {
		if pm := p.(map[string]any); pm["charts"] == nil {
			pm["charts"] = []any{}
		}
	}
	if err := writeYAML(filepath.Join(dir, "helmfile.discovery-map.yaml"), map[string]any{"releases": []any{}}); err != nil {
		return "", err
	}
	root := filepath.Join(dir, "helmfile.yaml")
	return root, writeYAML(root, map[string]any{"helmfiles": []any{map[string]any{
		"path": "helmfile.discovery-map.yaml", "values": []any{map[string]any{"atlasDiscovery": values}},
	}}})
}
