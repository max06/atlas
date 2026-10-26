package engine

import (
	"fmt"
	"os"
	"path"
	"strings"
)

// Sops decrypts one SOPS file (the templates' fetchSecretValue
// "ref+sops://<path>?format=yaml"), returning the decrypted YAML text.
type Sops func(path string) (string, error)

// Context is the input of one hierarchy walk: the .Values of the state
// (the atlas object with atlas.deployment set), the .Release in scope.
type Context struct {
	Values  map[string]any
	Release map[string]any
	// Redact rewrites every decrypted tree through the redaction
	// (atlas.redact.value) before the merge; SkipSecrets reads SOPS files
	// without decryption (their ENC[...] values, no sops metadata).
	Redact, SkipSecrets bool
}

func atlasOf(values map[string]any) (map[string]any, error) {
	a, ok := values["atlas"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no atlas object in .Values")
	}
	return a, nil
}

func atlasString(atlas map[string]any, key string) string {
	return fmt.Sprintf("%s", atlas[key])
}

// sopsLoad is atlas.sops.load: one SOPS file as a value tree.
func (e *Engine) sopsLoad(file string, redact, skipSecrets bool) (map[string]any, error) {
	var loaded map[string]any
	if skipSecrets {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		m, err := fromYamlMap(string(data), file)
		if err != nil {
			return nil, err
		}
		loaded = omit(m, "sops")
	} else {
		if e.Sops == nil {
			return nil, fmt.Errorf("%s: no SOPS decryption configured", file)
		}
		text, err := e.Sops(file)
		if err != nil {
			return nil, err
		}
		if loaded, err = fromYamlMap(text, file); err != nil {
			return nil, err
		}
		if redact {
			if loaded, err = e.redactValue(loaded); err != nil {
				return nil, err
			}
		}
	}
	return roundTrip(loaded, file)
}

// redactValue is atlas.redact.value (step C).
func (e *Engine) redactValue(v map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("native engine: redaction is not implemented yet")
}

// Hierarchy is atlas.hierarchy.merged: global → group → cluster →
// deployment values files, each kind in the order sops, plain, gotmpl.
// A gotmpl file sees the atlas object, the hierarchy so far (bare and
// under .Values) and .Release.
func (e *Engine) Hierarchy(c Context) (map[string]any, error) {
	atlas, err := atlasOf(c.Values)
	if err != nil {
		return nil, err
	}
	deployment, _ := atlas["deployment"].(map[string]any)
	hierarchyDir := atlasString(atlas, "cwd") + "/" + atlasString(atlas, "deploymentDefinitions")
	deploymentDir := path.Dir(fmt.Sprintf("%s", deployment["deploymentPath"]))
	cluster := fmt.Sprintf("%s", deployment["cluster"])

	files := []string{
		hierarchyDir + "/global.values.sops.yaml",
		hierarchyDir + "/global.values.yaml",
		hierarchyDir + "/global.values.yaml.gotmpl",
	}
	if strings.Contains(cluster, "/") {
		group := path.Dir(cluster)
		files = append(files,
			hierarchyDir+"/"+group+"/group.values.sops.yaml",
			hierarchyDir+"/"+group+"/group.values.yaml",
			hierarchyDir+"/"+group+"/group.values.yaml.gotmpl")
	}
	files = append(files,
		hierarchyDir+"/"+cluster+"/cluster.values.sops.yaml",
		hierarchyDir+"/"+cluster+"/cluster.values.yaml",
		hierarchyDir+"/"+cluster+"/cluster.values.yaml.gotmpl",
		deploymentDir+"/values.sops.yaml",
		deploymentDir+"/values.yaml",
		deploymentDir+"/values.yaml.gotmpl")

	hierarchy := map[string]any{}
	for _, f := range files {
		ok, err := isFile(f)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		switch {
		case strings.HasSuffix(f, ".sops.yaml"):
			v, err := e.sopsLoad(f, c.Redact, c.SkipSecrets)
			if err != nil {
				return nil, err
			}
			hierarchy = merge(hierarchy, v)
		case strings.HasSuffix(f, ".yaml.gotmpl"):
			ctx := merge(deepCopy(c.Values), deepCopy(hierarchy))
			ctx["Release"] = c.Release
			ctx["Values"] = deepCopy(ctx)
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			out, err := e.T.Tpl(string(data), ctx)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			v, err := fromYamlMap(out, f)
			if err != nil {
				return nil, err
			}
			hierarchy = merge(hierarchy, v)
		default:
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			v, err := fromYamlMap(string(data), f)
			if err != nil {
				return nil, err
			}
			hierarchy = merge(hierarchy, v)
		}
	}
	return roundTrip(hierarchy, "hierarchy")
}
