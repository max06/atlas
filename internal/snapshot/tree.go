// Package snapshot works on rendered review trees: the output of
// `helmfile template --output-dir <root> --output-dir-template
// '{{.OutputDir}}/<cluster>/<deployment>/<release>'`, one directory per
// release, the chart's files below it (<chart>/templates/…, <chart>/crds/…).
//
// It finds the releases of a tree, splits multi-document files into one file
// per resource, replays redaction maps over an older baseline (scrub), and
// diffs two trees resource by resource.
package snapshot

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Releases returns the release paths (<cluster…>/<deployment>/<release>,
// slash-separated, relative to root) of a render tree, sorted.
//
// With the (cluster, deployment) pairs of the render ("<cluster>|<deployment>",
// from the discovery map or the rendered pair list) the answer is exact:
// every directory below <cluster>/<deployment> is a release. Without pairs
// the release directories are guessed (Guess) — the fallback for trees whose
// pairs are unknown.
func Releases(root string, pairs []string) ([]string, error) {
	if pairs == nil {
		return Guess(root)
	}
	seen := map[string]bool{}
	for _, p := range pairs {
		cluster, deployment, ok := strings.Cut(p, "|")
		if !ok || cluster == "" || deployment == "" {
			continue
		}
		prefix := cluster + "/" + deployment
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(prefix)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				seen[prefix+"/"+e.Name()] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out, nil
}

// Guess finds the release directories of a render tree without knowing its
// pairs.
//
// A release directory holds one directory per chart, and helm writes a chart's
// manifests into <chart>/templates and its CRDs into <chart>/crds. So a
// "templates" or "crds" directory names a release two levels up — unless it
// has a "templates" directory of its own (then it is a chart that happens to
// be called that: cert-manager's CRD chart is "crds"), or the candidate is
// less than three levels deep (<cluster>/<deployment>/<release>: then the
// directory is a release or deployment of that name). Subcharts sit deeper
// (<release>/<chart>/charts/<sub>/templates): a candidate below another
// candidate is part of that release.
//
// The guess cannot tell every layout apart (an umbrella chart without own
// templates looks like a release <release>/<chart>/charts); callers that
// replay redaction maps fail closed on paths a map exists above or below
// (see Scrub). The bash pipeline matched "templates" only, so it listed every
// subchart as an extra release and missed charts that render CRDs only.
func Guess(root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || (d.Name() != "templates" && d.Name() != "crds") {
			return nil
		}
		if st, err := os.Stat(filepath.Join(path, "templates")); err == nil && st.IsDir() {
			return nil // a chart named "templates" or "crds"
		}
		rel, err := filepath.Rel(root, filepath.Dir(filepath.Dir(path)))
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || strings.Count(rel, "/") < 2 {
			return nil // too shallow for <cluster>/<deployment>/<release>
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	all := make([]string, 0, len(seen))
	for r := range seen {
		all = append(all, r)
	}
	sort.Strings(all)
	// drop candidates nested in another candidate (subcharts, templates/crds)
	var out []string
	for _, r := range all {
		nested := false
		for p := path.Dir(r); p != "." && p != "/"; p = path.Dir(p) {
			if seen[p] {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, r)
		}
	}
	return out, nil
}

// SplitRelease splits a release path into cluster (may contain "/"),
// deployment and release name: the last two segments are deployment and
// release, the rest is the cluster.
func SplitRelease(path string) (cluster, deployment, release string) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return "", "", path
	}
	n := len(parts)
	return strings.Join(parts[:n-2], "/"), parts[n-2], parts[n-1]
}

// ResourceFiles lists the *.yaml files below dir, relative and slash-separated, sorted.
func ResourceFiles(dir string) ([]string, error) {
	var out []string
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".yaml") {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}
