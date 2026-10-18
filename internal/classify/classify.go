// Package classify maps a change set onto the (cluster, deployment) pairs
// whose render can differ — the render subset of a review.
//
// The ATLAS directory convention IS the dependency graph, so changed paths map
// mechanically onto pairs. Rules (per changed path; selections are unioned,
// any "full" wins):
//
//	entry helmfile                          full   (the pipeline itself changed)
//	<templates>/<t>/**                      pairs instantiating template <t>; <t> is
//	                                        matched against the template names the maps
//	                                        know, so it may span directories
//	<templates>/<ns>/<file>                 a file under no known template selects every
//	                                        template under its nearest ancestor directory
//	                                        that holds known templates; none → nothing
//	<templates>/<file>                      full   (a file directly in the templates root)
//	<deployments>/<prefix>/apps/<name>/**   deployment <name> on every leaf cluster
//	                                        under <prefix> ("" = every cluster)
//	<deployments>/<prefix>/<file>           every deployment of every cluster under
//	                                        <prefix>; <prefix> == "" → full
//	<chart>/**                              pairs whose releases render from local chart
//	                                        <chart> (needs v2 maps on both sides)
//	anything else                           full   (default-deny)
//
// plus: pairs that exist on only ONE revision are always selected.
//
// Selection is over-approximate by design: over-selection costs render time,
// under-selection is a silent green review.
//
// This is a port of .github/actions/atlas-render/classify.sh; Result
// serializes to the same classify.json, so both can be compared directly.
package classify

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gitrev"
)

// Change is a classified changed path.
type Change struct {
	Status string   `json:"status"`
	Path   string   `json:"path"`
	Full   bool     `json:"full"`
	Reason string   `json:"reason,omitempty"`
	Rule   string   `json:"rule,omitempty"`
	Detail string   `json:"detail,omitempty"`
	Pairs  []string `json:"pairs"`

	classified bool // false: precondition forced a full render, no rule applied
}

// MarshalJSON emits only status+path for unclassified changes, like classify.sh.
func (c Change) MarshalJSON() ([]byte, error) {
	if !c.classified {
		return json.Marshal(struct {
			Status string `json:"status"`
			Path   string `json:"path"`
		}{c.Status, c.Path})
	}
	type plain Change
	return json.Marshal(plain(c))
}

// Result is the classification of one change set.
type Result struct {
	Mode   string `json:"mode"` // "subset" or "full"
	Reason string `json:"reason"`
	// Changes are unclassified (no rule fields) when a precondition already
	// forced a full render.
	Changes          []Change  `json:"changes"`
	OneSideOnly      *[]string `json:"one_side_only,omitempty"`
	Selected         []string  `json:"selected"`
	SelectedBaseline *[]string `json:"selected_baseline,omitempty"`
	SelectedPR       *[]string `json:"selected_pr,omitempty"`
	BaseTotal        int       `json:"base_total"`
	PRTotal          int       `json:"pr_total"`
}

// Input is what a classification needs. Base or PR may be nil (that side has
// no map) — that forces a full render.
type Input struct {
	Base, PR *discovery.Map
	Entry    string // repo-relative entry helmfile
	Changes  []gitrev.Change
}

type pairEdges struct {
	cluster, deploymentName string
	templates, charts       []string
}

// Classify applies the rules.
func Classify(in Input) Result {
	raw := make([]Change, 0, len(in.Changes))
	for _, c := range in.Changes {
		raw = append(raw, Change{Status: c.Status, Path: c.Path})
	}
	if in.Base == nil || in.PR == nil {
		reason := "no discovery map on the merge result"
		switch {
		case in.Base == nil && in.PR == nil:
			reason = "no discovery map on either side"
		case in.Base == nil:
			reason = "no discovery map on the target branch"
		}
		return Result{Mode: "full", Reason: reason, Changes: raw, Selected: []string{}}
	}
	baseKeys, prKeys := keys(in.Base), keys(in.PR)
	if in.Base.DeploymentsRoot != in.PR.DeploymentsRoot || in.Base.TemplatesRoot != in.PR.TemplatesRoot {
		return Result{Mode: "full", Reason: "deployments/templates root changed between revisions",
			Changes: raw, Selected: []string{}, BaseTotal: len(baseKeys), PRTotal: len(prKeys)}
	}
	D, T := normRoot(in.PR.DeploymentsRoot), normRoot(in.PR.TemplatesRoot)

	// union of both maps, edges merged per pair, ordered by key
	byKey := map[string]*pairEdges{}
	for _, m := range []*discovery.Map{in.Base, in.PR} {
		for _, p := range m.Pairs {
			e := byKey[p.Key()]
			if e == nil {
				e = &pairEdges{cluster: p.Cluster, deploymentName: p.DeploymentName}
				byKey[p.Key()] = e
			}
			e.templates = append(e.templates, p.Templates...)
			e.charts = append(e.charts, p.Charts...)
		}
	}
	allKeys := sortedKeys(byKey)
	var knownTemplates, knownCharts []string
	for _, k := range allKeys {
		e := byKey[k]
		e.templates, e.charts = uniq(e.templates), uniq(e.charts)
		knownTemplates = append(knownTemplates, e.templates...)
		knownCharts = append(knownCharts, e.charts...)
	}
	knownTemplates, knownCharts = uniq(knownTemplates), uniq(knownCharts)
	chartAware := in.Base.Version >= 2 && in.PR.Version >= 2

	pairsWhere := func(pred func(*pairEdges) bool) []string {
		sel := []string{}
		for _, k := range allKeys {
			if pred(byKey[k]) {
				sel = append(sel, k)
			}
		}
		return sel
	}
	usesAny := func(have, want []string) bool {
		for _, h := range have {
			for _, w := range want {
				if h == w {
					return true
				}
			}
		}
		return false
	}
	full := func(c Change, reason string) Change {
		c.classified, c.Full, c.Reason, c.Pairs = true, true, reason, []string{}
		return c
	}
	selectPairs := func(c Change, rule, detail string, pairs []string) Change {
		c.classified, c.Rule, c.Detail, c.Pairs = true, rule, detail, pairs
		return c
	}

	classified := make([]Change, 0, len(raw))
	for _, c := range raw {
		p := c.Path
		switch {
		case p == in.Entry:
			c = full(c, "entry helmfile changed")

		case under(p, T):
			rel := stripRoot(p, T)
			segs := strings.Split(rel, "/")
			if len(segs) < 2 {
				c = full(c, "file directly in the templates root: "+p)
				break
			}
			var hits []string
			for _, t := range knownTemplates {
				if strings.HasPrefix(rel, t+"/") {
					hits = append(hits, t)
				}
			}
			if len(hits) > 0 {
				c = selectPairs(c, "template", strings.Join(hits, ", "),
					pairsWhere(func(e *pairEdges) bool { return usesAny(e.templates, hits) }))
				break
			}
			// A file inside no known template: a helper shared by namespaced
			// templates, or a file of a template nothing uses. The nearest
			// ancestor holding known templates is the namespace that may read it.
			found := false
			for n := len(segs) - 1; n >= 1 && !found; n-- {
				ns := strings.Join(segs[:n], "/")
				var members []string
				for _, t := range knownTemplates {
					if strings.HasPrefix(t, ns+"/") {
						members = append(members, t)
					}
				}
				if len(members) > 0 {
					found = true
					c = selectPairs(c, "template-namespace", ns+"/*",
						pairsWhere(func(e *pairEdges) bool { return usesAny(e.templates, members) }))
				}
			}
			if !found {
				c = selectPairs(c, "template", segs[0], []string{})
			}

		case under(p, D):
			segs := strings.Split(stripRoot(p, D), "/")
			apps := -1
			for i, s := range segs {
				if s == "apps" {
					apps = i
					break
				}
			}
			if apps >= 0 {
				prefix := strings.Join(segs[:apps], "/")
				name := ""
				if apps+1 < len(segs) {
					name = segs[apps+1]
				}
				if name == "" || len(segs) < apps+3 {
					// a file directly under apps/ is read by nothing
					c = selectPairs(c, "apps-dir-file", p, []string{})
					break
				}
				detail := prefix + "/apps/" + name
				if prefix == "" {
					detail = "apps/" + name
				}
				c = selectPairs(c, "deployment", detail, pairsWhere(func(e *pairEdges) bool {
					return e.deploymentName == name && clusterUnder(e.cluster, prefix)
				}))
				break
			}
			prefix := strings.Join(segs[:len(segs)-1], "/")
			if prefix == "" {
				c = full(c, "global hierarchy file changed: "+p)
				break
			}
			c = selectPairs(c, "hierarchy", prefix,
				pairsWhere(func(e *pairEdges) bool { return clusterUnder(e.cluster, prefix) }))

		default:
			var chartHits []string
			if chartAware {
				for _, ch := range knownCharts {
					if strings.HasPrefix(p, ch+"/") {
						chartHits = append(chartHits, ch)
					}
				}
			}
			if len(chartHits) > 0 {
				c = selectPairs(c, "chart", strings.Join(chartHits, ", "),
					pairsWhere(func(e *pairEdges) bool { return usesAny(e.charts, chartHits) }))
			} else {
				c = full(c, "outside deployments/templates: "+p)
			}
		}
		classified = append(classified, c)
	}

	for _, c := range classified {
		if c.Full {
			return Result{Mode: "full", Reason: c.Reason, Changes: classified, Selected: []string{},
				BaseTotal: len(baseKeys), PRTotal: len(prKeys)}
		}
	}

	baseSet, prSet := set(baseKeys), set(prKeys)
	oneSide := []string{}
	for _, k := range baseKeys {
		if !prSet[k] {
			oneSide = append(oneSide, k)
		}
	}
	for _, k := range prKeys {
		if !baseSet[k] {
			oneSide = append(oneSide, k)
		}
	}
	var all []string
	for _, c := range classified {
		all = append(all, c.Pairs...)
	}
	selected := uniq(append(all, oneSide...))
	selBase, selPR := []string{}, []string{}
	for _, k := range selected {
		if baseSet[k] {
			selBase = append(selBase, k)
		}
		if prSet[k] {
			selPR = append(selPR, k)
		}
	}
	return Result{Mode: "subset", Changes: classified, OneSideOnly: &oneSide, Selected: selected,
		SelectedBaseline: &selBase, SelectedPR: &selPR, BaseTotal: len(baseKeys), PRTotal: len(prKeys)}
}

// normRoot: "./deployments/" → "deployments".
func normRoot(r string) string {
	return strings.TrimRight(strings.TrimPrefix(r, "./"), "/")
}

func under(p, root string) bool {
	return root == "" || p == root || strings.HasPrefix(p, root+"/")
}

func stripRoot(p, root string) string {
	if root == "" {
		return p
	}
	return p[len(root)+1:]
}

func clusterUnder(cluster, prefix string) bool {
	return prefix == "" || cluster == prefix || strings.HasPrefix(cluster, prefix+"/")
}

func keys(m *discovery.Map) []string {
	out := make([]string, 0, len(m.Pairs))
	for _, p := range m.Pairs {
		out = append(out, p.Key())
	}
	return out
}

func sortedKeys(m map[string]*pairEdges) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// uniq sorts and deduplicates (jq's `unique`).
func uniq(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	s := append([]string(nil), in...)
	sort.Strings(s)
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func set(in []string) map[string]bool {
	m := make(map[string]bool, len(in))
	for _, v := range in {
		m[v] = true
	}
	return m
}
