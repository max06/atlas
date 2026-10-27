package snapshot

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Maps are the redaction maps of one app instance as the templates (or the
// native engine) compute them in ATLAS_REDACT_MAPS mode: per release, the
// plaintext → marker replacements.
type Maps struct {
	Version        int                       `yaml:"version" json:"version"`
	Cluster        string                    `yaml:"cluster" json:"cluster"`
	DeploymentName string                    `yaml:"deploymentName" json:"deploymentName"`
	Releases       map[string]map[string]any `yaml:"releases" json:"releases"`
}

// WriteMaps writes one map file per release in the sidedump layout
// (<mapDir>/<cluster>/<deployment>/<release>.json, an empty map as {}), the
// layout Scrub and the replay read. It returns the number of releases
// written.
func WriteMaps(mapDir string, all []Maps) (int, error) {
	n := 0
	for _, c := range all {
		if c.Cluster == "" || c.DeploymentName == "" {
			return n, fmt.Errorf("redaction maps: a carrier without cluster or deployment name")
		}
		for release, m := range c.Releases {
			if m == nil {
				m = map[string]any{}
			}
			data, err := json.Marshal(m)
			if err != nil {
				return n, err
			}
			file := filepath.Join(mapDir, filepath.FromSlash(c.Cluster), c.DeploymentName, release+".json")
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return n, err
			}
			if err := os.WriteFile(file, append(data, '\n'), 0o644); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// RedactTree redacts a render tree in place with its own maps, as the
// review's render step does: releases without secrets get an empty map (the
// structural Secret pass runs for every release), then Scrub. Releases the
// scrubber could not handle ("no-map", "unscrubbed") are removed from the
// tree, so no plaintext leaves it; they are returned.
func RedactTree(root, mapDir string, pairs []string, warn io.Writer) ([]ScrubStatus, error) {
	releases, err := Releases(root, pairs)
	if err != nil {
		return nil, err
	}
	for _, r := range releases {
		mapFile := filepath.Join(mapDir, filepath.FromSlash(r)+".json")
		if _, err := os.Stat(mapFile); err == nil || MapAmbiguous(mapDir, r) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(mapFile), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(mapFile, []byte("{}\n"), 0o644); err != nil {
			return nil, err
		}
	}
	statuses, err := Scrub(root, mapDir, pairs, warn)
	if err != nil {
		return nil, err
	}
	var failed []ScrubStatus
	for _, st := range statuses {
		if st.Status != "no-map" && st.Status != "unscrubbed" {
			continue
		}
		failed = append(failed, st)
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(st.Release))); err != nil {
			return failed, err
		}
	}
	return failed, nil
}
