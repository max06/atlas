package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/helmfile/helmfile/pkg/tmpl"
)

// GenerateOptions control a generated render.
type GenerateOptions struct {
	// NamespaceOverride is helmfile's --namespace: it becomes the
	// .Release.Namespace of every values computation as well.
	NamespaceOverride string
}

// Generate writes the native render of the selected pairs into dir: one
// state file per app instance (states/NNNN.yaml, in the template engine's
// order), the values of each release (values/<cluster>/<deployment>/
// <instance>/<release>.yaml) and the root state helmfile.yaml that lists
// the instance states. helmfile renders it with no ATLAS template left.
func (e *Engine) Generate(dir string, f Filter, o GenerateOptions) (string, error) {
	pairs, err := Discover(e.Layout, f)
	if err != nil {
		return "", err
	}
	for _, sub := range []string{"states", "values"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	if err := writeYAML(filepath.Join(dir, "states", CarrierFile), map[string]any{"releases": []any{}}); err != nil {
		return "", err
	}
	var states []any
	for _, p := range pairs {
		instances, err := e.Instances(p)
		if err != nil {
			return "", err
		}
		for _, inst := range instances {
			for _, release := range inst.Releases {
				rel := ReleaseContext(release, o.NamespaceOverride)
				vals, err := e.Values(deepCopy(inst.Values), rel, false)
				if err != nil {
					return "", fmt.Errorf("%s/%s release %s: %w", p.Cluster, p.DeploymentName, rel["Name"], err)
				}
				file := filepath.Join(dir, "values", p.Cluster, p.DeploymentName, inst.Name, fmt.Sprintf("%s.yaml", rel["Name"]))
				if err := writeYAML(file, vals); err != nil {
					return "", err
				}
				list := release["values"].([]any)
				list[len(list)-1] = file
			}
			name := fmt.Sprintf("states/%04d.yaml", len(states)+1)
			if err := writeState(filepath.Join(dir, name), inst.State); err != nil {
				return "", err
			}
			states = append(states, map[string]any{"path": name})
		}
	}
	root := filepath.Join(dir, "helmfile.yaml")
	if states == nil {
		states = []any{}
	}
	return root, writeYAML(root, map[string]any{"helmfiles": states})
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
	env, err := tmpl.ToYaml(map[string]any{"environments": state["environments"]})
	if err != nil {
		return err
	}
	body, err := tmpl.ToYaml(rest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(env+"---\n"+body), 0o644)
}

func writeYAML(file string, v any) error {
	s, err := tmpl.ToYaml(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(s), 0o644)
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
