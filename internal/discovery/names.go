package discovery

import (
	"fmt"
	"sort"
	"strings"
)

// Clusters returns the distinct cluster paths of the map, sorted.
func (m *Map) Clusters() []string {
	return m.distinct(func(p Pair) string { return p.Cluster })
}

// Deployments returns the distinct deployment names of the map, sorted.
func (m *Map) Deployments() []string {
	return m.distinct(func(p Pair) string { return p.DeploymentName })
}

func (m *Map) distinct(key func(Pair) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range m.Pairs {
		if k := key(p); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ResolveCluster maps a cluster name as a person types it to the full
// cluster path: the full path itself, or the leaf name when exactly one
// cluster carries it (leaf names are unique per repo, ATLAS enforces it).
func (m *Map) ResolveCluster(name string) (string, error) {
	var leafHits []string
	for _, c := range m.Clusters() {
		if c == name {
			return c, nil
		}
		if leaf(c) == name {
			leafHits = append(leafHits, c)
		}
	}
	if len(leafHits) == 1 {
		return leafHits[0], nil
	}
	return "", unknown("cluster", name, m.Clusters())
}

// CheckDeployment returns an error with suggestions when no pair of the map
// has deployment name.
func (m *Map) CheckDeployment(name string) error {
	for _, d := range m.Deployments() {
		if d == name {
			return nil
		}
	}
	return unknown("deployment", name, m.Deployments())
}

func leaf(cluster string) string {
	if i := strings.LastIndex(cluster, "/"); i >= 0 {
		return cluster[i+1:]
	}
	return cluster
}

// unknown names up to five candidates that contain the name (or that the
// name contains), else lists the first known ones.
func unknown(kind, name string, known []string) error {
	var near []string
	for _, k := range known {
		if strings.Contains(k, name) || strings.Contains(name, leaf(k)) {
			near = append(near, k)
		}
	}
	switch {
	case len(near) > 0:
		return fmt.Errorf("unknown %s %q — did you mean: %s", kind, name, strings.Join(first(near, 5), ", "))
	case len(known) > 0:
		return fmt.Errorf("unknown %s %q — known: %s", kind, name, strings.Join(first(known, 8), ", "))
	default:
		return fmt.Errorf("unknown %s %q — the repository has none", kind, name)
	}
}

func first(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	return append(list[:n:n], fmt.Sprintf("… (%d more)", len(list)-n))
}
