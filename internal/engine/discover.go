// Package engine is the native ATLAS engine: the logic of the helmfile
// templates (templates/*.gotmpl, templates/_*.tpl) in Go.
//
// Each part is a step-by-step port of its template, checked against the
// template engine (same discovery map, same values per release). Where the
// templates call sprig or helmfile functions, the port calls the same
// functions (helmfile's pkg/tmpl, sprig), so their corner cases stay the
// same.
package engine

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Layout is the tree the engine works on: the atlas.cwd,
// atlas.appTemplates and atlas.deploymentDefinitions values of the entry.
type Layout struct {
	Root        string // absolute repository root (atlas.cwd)
	Templates   string // app templates directory, relative to Root
	Deployments string // deployment definitions directory, relative to Root
}

// TemplatesDir and DeploymentsDir are the directories as the templates
// build them (printf "%s/%s"): not cleaned, so rendered paths stay equal.
func (l Layout) TemplatesDir() string   { return l.Root + "/" + l.Templates }
func (l Layout) DeploymentsDir() string { return l.Root + "/" + l.Deployments }

// Filter is the stage-1 filter (ATLAS_FILTER_CLUSTER,
// ATLAS_FILTER_DEPLOYMENT_NAME): exact cluster paths and deployment names.
type Filter struct {
	Clusters, Deployments []string
}

// Pair is one deployment on one cluster: atlas.deployment of the templates.
type Pair struct {
	Cluster        string // full path: "group/cluster" or "cluster"
	ClusterName    string // leaf name
	ClusterGroup   string // "" for a cluster without a group
	DeploymentName string
	DeploymentPath string // absolute path of deployment.yaml (as built by the glob)
}

// Discover is STEP 1 and STEP 2 of templates/helmfile.all.yaml.gotmpl: the
// leaf clusters (directories with an apps/ directory and no child cluster)
// and the deployments of each cluster (global, group and cluster level, the
// most specific definition of a name wins), in the template's order.
func Discover(l Layout, f Filter) ([]Pair, error) {
	if !filepath.IsAbs(l.Root) {
		return nil, fmt.Errorf("No valid cwd given")
	}
	if ok, err := isDir(l.Root); err != nil || !ok {
		return nil, fmt.Errorf("No valid cwd given")
	}
	dd := l.DeploymentsDir()

	raw, err := glob(dd + "/**/apps")
	if err != nil {
		return nil, err
	}
	clusters := map[string]bool{}
	leafSeen := map[string][]string{}
	for _, match := range raw {
		clusterPath := path.Dir(strings.TrimPrefix(match, dd+"/"))
		// "." is the global apps/ directory itself
		if clusterPath == "" || clusterPath == "." {
			continue
		}
		children, err := glob(dd + "/" + clusterPath + "/*/apps")
		if err != nil {
			return nil, err
		}
		leaf := len(children) == 0
		if leaf {
			// before the filter: name collisions are a repository problem
			name := path.Base(clusterPath)
			leafSeen[name] = append(leafSeen[name], clusterPath)
		}
		if leaf && (len(f.Clusters) == 0 || has(f.Clusters, clusterPath)) {
			clusters[clusterPath] = true
		}
	}
	for _, name := range sortedKeys(leafSeen) {
		if paths := leafSeen[name]; len(paths) > 1 {
			return nil, fmt.Errorf("cluster discovery: duplicate leaf cluster name %q used by: %s — leaf names must be unique across the repo (clusterName names downstream resources)", name, strings.Join(paths, ", "))
		}
	}

	var pairs []Pair
	for _, cluster := range sortedKeys(clusters) {
		patterns := []string{dd + "/apps/*/deployment.yaml"}
		if group := path.Dir(cluster); group != "." {
			patterns = append(patterns, dd+"/"+group+"/apps/*/deployment.yaml")
		}
		patterns = append(patterns, dd+"/"+cluster+"/apps/*/deployment.yaml")
		byName := map[string]string{}
		for _, p := range patterns {
			found, err := glob(p)
			if err != nil {
				return nil, err
			}
			for _, d := range found {
				byName[path.Base(path.Dir(d))] = d // later (more specific) wins
			}
		}
		for _, name := range sortedKeys(byName) {
			if len(f.Deployments) > 0 && !has(f.Deployments, name) {
				continue
			}
			p := Pair{Cluster: cluster, ClusterName: path.Base(cluster), DeploymentName: name, DeploymentPath: byName[name]}
			if strings.Contains(cluster, "/") {
				p.ClusterGroup = path.Dir(cluster)
			}
			pairs = append(pairs, p)
		}
	}
	return pairs, nil
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
