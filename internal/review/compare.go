package review

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReleaseChange is one release whose rendered files differ between the sides.
type ReleaseChange struct {
	Release string   `json:"release"` // <cluster>/<deployment>/<release>
	Kind    string   `json:"kind"`    // added, removed, changed
	Files   []string `json:"files"`   // differing files, relative to the release dir
}

// Compare lists the releases that differ between two render trees laid out as
// <cluster>/<deployment>/<release>/... . Clusters may contain "/" (groups), so
// release directories are identified from the known pairs ("<cluster>|<deployment>").
func Compare(baseDir, headDir string, pairs []string) ([]ReleaseChange, error) {
	prefixes := make([]string, 0, len(pairs))
	for _, k := range pairs {
		cluster, deployment, _ := strings.Cut(k, "|")
		prefixes = append(prefixes, cluster+"/"+deployment+"/")
	}
	// longest prefix first: group1/cluster2/x must not match group1/x
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	release := func(rel string) (string, string) {
		for _, p := range prefixes {
			if strings.HasPrefix(rel, p) {
				name, rest, _ := strings.Cut(strings.TrimPrefix(rel, p), "/")
				return p + name, rest
			}
		}
		return filepath.Dir(rel), filepath.Base(rel) // unknown pair: per directory
	}

	files := func(root string) (map[string][]byte, error) {
		out := map[string][]byte{}
		if _, err := os.Stat(root); os.IsNotExist(err) {
			return out, nil
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			out[filepath.ToSlash(rel)] = data
			return nil
		})
		return out, err
	}
	baseFiles, err := files(baseDir)
	if err != nil {
		return nil, err
	}
	headFiles, err := files(headDir)
	if err != nil {
		return nil, err
	}

	type state struct {
		inBase, inHead bool
		files          []string
	}
	releases := map[string]*state{}
	get := func(r string) *state {
		if releases[r] == nil {
			releases[r] = &state{}
		}
		return releases[r]
	}
	for rel, data := range baseFiles {
		r, f := release(rel)
		st := get(r)
		st.inBase = true
		if other, ok := headFiles[rel]; !ok || !bytes.Equal(data, other) {
			st.files = append(st.files, f)
		}
	}
	for rel := range headFiles {
		r, f := release(rel)
		st := get(r)
		st.inHead = true
		if _, ok := baseFiles[rel]; !ok {
			st.files = append(st.files, f)
		}
	}
	var out []ReleaseChange
	for r, st := range releases {
		if len(st.files) == 0 {
			continue
		}
		kind := "changed"
		switch {
		case !st.inBase:
			kind = "added"
		case !st.inHead:
			kind = "removed"
		}
		sort.Strings(st.files)
		out = append(out, ReleaseChange{Release: r, Kind: kind, Files: st.files})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Release < out[j].Release })
	return out, nil
}
