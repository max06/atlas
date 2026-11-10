package ci

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/max06/atlas/internal/engine"
	"github.com/max06/atlas/internal/native"
	"github.com/max06/atlas/internal/renderer"
	"github.com/max06/atlas/internal/snapshot"

	"go.yaml.in/yaml/v3"
)

// InventoryVersion is the version of the inventory.json layout. Raise it when
// a field changes its meaning or disappears; new fields keep it.
const InventoryVersion = 1

// Inventory is inventory.json: what the merge result deploys, for tools that
// judge a change (which chart versions, images and CRD versions exist where).
// It holds names, versions and repository URLs only, never values.
type Inventory struct {
	Version int              `json:"version"`
	Charts  *InventoryCharts `json:"charts,omitempty"`
	Images  InventoryImages  `json:"images"`
	CRDs    InventoryCRDs    `json:"crds"`
	// Notes say what is missing and why (for example a tree without the
	// native engine has no chart list).
	Notes []string `json:"notes,omitempty"`
}

// InventoryCharts are the charts of every pair of the tree, from a keyless
// generation of the states (no render).
type InventoryCharts struct {
	Pairs        int               `json:"pairs"`                  // pairs of the tree
	FailedPairs  []string          `json:"failedPairs,omitempty"`  // pairs without states
	KubeVersions map[string]string `json:"kubeVersions,omitempty"` // cluster → kubeVersion of its states
	Items        []InventoryChart  `json:"items"`
}

// InventoryChart is one chart in one version with the releases that use it.
type InventoryChart struct {
	Chart      string `json:"chart"`                // chart name, or the repo-relative path of a local chart
	Repository string `json:"repository,omitempty"` // repository URL (credentials removed); empty for a local chart
	Version    string `json:"version,omitempty"`    // empty: no version pinned (local charts, latest)
	// Releases: "<cluster>|<deployment>|<release>", sorted.
	Releases []string `json:"releases"`
}

// InventoryImages are the images of the rendered pairs (merge-result tree).
type InventoryImages struct {
	RenderMode string           `json:"renderMode"` // full / subset: subset covers only the rendered pairs
	Pairs      int              `json:"pairs"`
	Items      []InventoryImage `json:"items"`
}

// InventoryImage is one image reference with the resources that use it.
type InventoryImage struct {
	Image string `json:"image"`
	// Resources: "<cluster>/<deployment>/<release>: <Kind>/<name>", sorted.
	Resources []string `json:"resources"`
}

// InventoryCRDs are the CustomResourceDefinitions of the rendered pairs.
type InventoryCRDs struct {
	RenderMode string         `json:"renderMode"`
	Pairs      int            `json:"pairs"`
	Items      []InventoryCRD `json:"items"`
}

// InventoryCRD is one CRD as one release renders it.
type InventoryCRD struct {
	Name     string                `json:"name"`
	Group    string                `json:"group"`
	Kind     string                `json:"kind"`
	Versions []InventoryCRDVersion `json:"versions"`
	Release  string                `json:"release"` // <cluster>/<deployment>/<release>
}

// InventoryCRDVersion is one version of a CRD.
type InventoryCRDVersion struct {
	Name    string `json:"name"`
	Served  bool   `json:"served"`
	Storage bool   `json:"storage"`
}

// InventoryInput is what BuildInventory reads.
type InventoryInput struct {
	Dir        string   // the tree of the merge result (checkout or workspace)
	TreeDir    string   // the merge result's render tree
	Pairs      []string // the rendered pairs ("<cluster>|<deployment>")
	RenderMode string   // full / subset
	Work       string   // scratch directory for the keyless states
}

// BuildInventory collects the charts (all pairs, keyless states of a native
// tree), images and CRDs (rendered pairs) of the merge result.
func BuildInventory(in InventoryInput, log io.Writer) (Inventory, error) {
	inv := Inventory{Version: InventoryVersion}
	if l, isNative, err := nativeLayout(in.Dir); err != nil {
		inv.Notes = append(inv.Notes, fmt.Sprintf("no chart list: %v", err))
	} else if !isNative {
		inv.Notes = append(inv.Notes, "no chart list: the tree does not use the native engine (atlas.yaml spec.native)")
	} else {
		charts, err := inventoryCharts(l, in.Dir, in.Work)
		if err != nil {
			inv.Notes = append(inv.Notes, fmt.Sprintf("no chart list: %v", err))
		} else {
			inv.Charts = charts
		}
	}
	images, crds, err := inventoryTree(in.TreeDir, in.Pairs)
	if err != nil {
		return inv, err
	}
	mode := firstNonEmpty(in.RenderMode, "full")
	inv.Images = InventoryImages{RenderMode: mode, Pairs: len(in.Pairs), Items: images}
	inv.CRDs = InventoryCRDs{RenderMode: mode, Pairs: len(in.Pairs), Items: crds}
	n := 0
	if inv.Charts != nil {
		n = len(inv.Charts.Items)
	}
	fmt.Fprintf(log, "inventory: %d chart version(s), %d image(s), %d CRD(s)\n", n, len(images), len(crds))
	return inv, nil
}

func inventoryCharts(l engine.Layout, dir, work string) (*InventoryCharts, error) {
	root, pairs, failed, err := native.GenerateKeyless(l, work)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	out := &InventoryCharts{Pairs: len(pairs), KubeVersions: map[string]string{}}
	for p := range failed {
		out.FailedPairs = append(out.FailedPairs, p)
	}
	sort.Strings(out.FailedPairs)
	byKey := map[[3]string]map[string]bool{}
	for _, st := range root.States {
		cluster, dep := st.Pair()
		if cluster == "" {
			continue // the carrier state
		}
		if st.KubeVersion != "" {
			out.KubeVersions[cluster] = st.KubeVersion
		}
		for _, r := range st.Releases {
			name, repo := chartSource(r.Chart, st.Repositories, abs)
			k := [3]string{name, repo, r.Version}
			if byKey[k] == nil {
				byKey[k] = map[string]bool{}
			}
			byKey[k][cluster+"|"+dep+"|"+r.Name] = true
		}
	}
	for k, rels := range byKey {
		out.Items = append(out.Items, InventoryChart{Chart: k[0], Repository: k[1], Version: k[2], Releases: sortedSet(rels)})
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Chart != b.Chart {
			return a.Chart < b.Chart
		}
		if a.Repository != b.Repository {
			return a.Repository < b.Repository
		}
		return a.Version < b.Version
	})
	if len(out.KubeVersions) == 0 {
		out.KubeVersions = nil
	}
	return out, nil
}

// chartSource splits a release's chart reference into the chart name and its
// repository URL: "<alias>/<name>" with the state's repository of that
// alias, "oci://<registry>/<path>/<name>", or a local chart directory (an
// absolute path below the tree becomes repo-relative, the repository stays
// empty).
func chartSource(chart string, repos []renderer.Repository, treeDir string) (name, repository string) {
	if rest, ok := strings.CutPrefix(chart, "oci://"); ok {
		i := strings.LastIndex(rest, "/")
		if i < 0 {
			return rest, "oci://"
		}
		return rest[i+1:], cleanURL("oci://" + rest[:i])
	}
	if filepath.IsAbs(chart) {
		if rel, err := filepath.Rel(treeDir, chart); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel), ""
		}
		return filepath.Base(chart), "" // outside the tree: never print the runner path
	}
	if alias, n, ok := strings.Cut(chart, "/"); ok {
		for _, r := range repos {
			if r.Name == alias {
				u := cleanURL(r.URL)
				if r.OCI && !strings.HasPrefix(u, "oci://") {
					u = "oci://" + u
				}
				return n, u
			}
		}
	}
	return chart, ""
}

// cleanURL removes credentials (user:password@) and the query from a URL.
func cleanURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		if i := strings.LastIndex(raw, "@"); i >= 0 {
			return raw[i+1:] // "user:pass@host/path" without a scheme
		}
		return raw
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// inventoryTree reads the images and CRDs of the rendered releases.
func inventoryTree(tree string, pairs []string) ([]InventoryImage, []InventoryCRD, error) {
	images := map[string]map[string]bool{}
	var crds []InventoryCRD
	if !nonEmpty(tree) {
		return []InventoryImage{}, []InventoryCRD{}, nil
	}
	releases, err := snapshot.Releases(tree, pairs)
	if err != nil {
		return nil, nil, err
	}
	for _, rel := range releases {
		root := filepath.Join(tree, filepath.FromSlash(rel))
		err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !e.Type().IsRegular() || !(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			dec := yaml.NewDecoder(bytes.NewReader(data))
			for {
				var doc map[string]any
				if err := dec.Decode(&doc); err != nil {
					if errors.Is(err, io.EOF) {
						return nil
					}
					return nil // not YAML a resource can be read from: skip the rest of the file
				}
				if doc == nil {
					continue
				}
				kind, _ := doc["kind"].(string)
				meta, _ := doc["metadata"].(map[string]any)
				name, _ := meta["name"].(string)
				where := rel + ": " + kind + "/" + name
				collectImages(doc, func(img string) {
					if images[img] == nil {
						images[img] = map[string]bool{}
					}
					images[img][where] = true
				})
				if kind == "CustomResourceDefinition" {
					crds = append(crds, crdOf(doc, name, rel))
				}
			}
		})
		if err != nil {
			return nil, nil, err
		}
	}
	out := []InventoryImage{}
	for img, where := range images {
		out = append(out, InventoryImage{Image: img, Resources: sortedSet(where)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Image < out[j].Image })
	sort.Slice(crds, func(i, j int) bool {
		if crds[i].Name != crds[j].Name {
			return crds[i].Name < crds[j].Name
		}
		return crds[i].Release < crds[j].Release
	})
	if crds == nil {
		crds = []InventoryCRD{}
	}
	return out, crds, nil
}

// collectImages calls add for each string value of an "image" key anywhere in
// the document (containers, init containers, and operator CRs such as a
// CephCluster's cephVersion.image). A CRD's schema is skipped: its "image"
// keys are property definitions, not images.
func collectImages(v any, add func(string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "openAPIV3Schema" {
				continue
			}
			if k == "image" {
				if s, ok := child.(string); ok && s != "" && !strings.ContainsAny(s, " \n{}") {
					add(s)
					continue
				}
			}
			collectImages(child, add)
		}
	case []any:
		for _, child := range t {
			collectImages(child, add)
		}
	}
}

func crdOf(doc map[string]any, name, rel string) InventoryCRD {
	c := InventoryCRD{Name: name, Release: rel, Versions: []InventoryCRDVersion{}}
	spec, _ := doc["spec"].(map[string]any)
	c.Group, _ = spec["group"].(string)
	if names, ok := spec["names"].(map[string]any); ok {
		c.Kind, _ = names["kind"].(string)
	}
	versions, _ := spec["versions"].([]any)
	for _, v := range versions {
		m, _ := v.(map[string]any)
		n, _ := m["name"].(string)
		served, _ := m["served"].(bool)
		storage, _ := m["storage"].(bool)
		c.Versions = append(c.Versions, InventoryCRDVersion{Name: n, Served: served, Storage: storage})
	}
	return c
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
