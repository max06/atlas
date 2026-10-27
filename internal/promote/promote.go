// Package promote plans the promotion of changes from one branch of a tree
// to another (for example canary → main) by unit instead of by branch.
//
// A unit is a ripple set: a change includes everything its effect touches.
// The smallest parts that can be copied are atoms (a deployment directory, a
// template, a local chart, the hierarchy files of one level, a top-level
// directory). Atoms that reach the same (cluster, deployment) pair on the
// target are linked; the linked atoms form a unit. With Split, a render on
// the target separates linked atoms whose renders do not interact.
//
// Each unit gets a content check against the target (plan 12 of atlas-next):
// the unit's files applied on the target with the merge base of the two
// branches (git merge-tree): "applies", "conflicts" or "present". Content
// only: squash merges remove the commit ancestry.
package promote

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gitrev"
	"github.com/max06/atlas/internal/review"
)

// Unit status.
const (
	Applies   = "applies"
	Conflicts = "conflicts"
	Present   = "present"
)

// Options of a plan.
type Options struct {
	Repo     *gitrev.Repo
	From, To string // revisions as they are in the clone
	Entry    string // entry helmfile, repository-relative
	Helmfile string // helmfile binary ("" = helmfile)
	// SkipClusters are not deployed from To (full path or leaf name): their
	// pairs do not count as affected and do not link atoms.
	SkipClusters []string
	// Split separates the atoms of a unit by render (Split in split.go).
	Split bool
	// Work is a private directory for the exported trees and renders; it
	// is created and removed.
	Work string
	Log  io.Writer
}

// Rev is a revision as given and resolved.
type Rev struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
}

// Plan is the result.
type Plan struct {
	From      Rev    `json:"from"`
	To        Rev    `json:"to"`
	MergeBase string `json:"mergeBase"`
	Units     []Unit `json:"units"`
	// Present: files changed on From since the merge base whose content To
	// already has. ToOnly: files changed only on To (the way back, not a
	// promotion).
	Present         int      `json:"present"`
	ToOnly          []string `json:"toOnly"`
	SkippedClusters []string `json:"skippedClusters"`
	Notes           []string `json:"notes,omitempty"`
}

// Unit is one promotable set of changes.
type Unit struct {
	ID     string `json:"id"` // the sorted atom ids, joined with " + "
	Status string `json:"status"`
	Atoms  []Atom `json:"atoms"`
	// Files with their change on From since the merge base (A, M, D).
	Files     []gitrev.Change `json:"files"`
	Conflicts []string        `json:"conflicts,omitempty"`
	// Pairs ("<cluster>|<deployment>") the unit can change on To; NewPairs
	// exist only on From (new deployments); SkippedPairs are on skipped
	// clusters.
	Pairs        []string `json:"pairs"`
	NewPairs     []string `json:"newPairs,omitempty"`
	SkippedPairs []string `json:"skippedPairs,omitempty"`
	// Full: the unit can change every pair (why).
	Full string `json:"full,omitempty"`
	// NoEffect: no pair on To changes (none is reached, or with Split the
	// render shows no change) and no new pair appears on a cluster deployed
	// from To.
	NoEffect bool   `json:"noEffect"`
	Split    *Split `json:"split,omitempty"`
}

// Atom is the smallest part of a change that can be copied on its own.
type Atom struct {
	ID           string   `json:"id"`
	Files        []string `json:"files"`
	Pairs        []string `json:"pairs"`
	NewPairs     []string `json:"newPairs,omitempty"`
	SkippedPairs []string `json:"skippedPairs,omitempty"`
	Full         string   `json:"full,omitempty"`

	changes []gitrev.Change
}

// added: every file of the atom is new (To does not have the atom yet).
func (a *Atom) added() bool {
	for _, c := range a.changes {
		if c.Status != "A" {
			return false
		}
	}
	return len(a.changes) > 0
}

// Make computes the plan.
func Make(ctx context.Context, o Options) (*Plan, error) {
	if o.Log == nil {
		o.Log = io.Discard
	}
	r := o.Repo
	p := &Plan{From: Rev{Ref: o.From}, To: Rev{Ref: o.To}, ToOnly: []string{}, Units: []Unit{}, SkippedClusters: append([]string{}, o.SkipClusters...)}
	var err error
	if p.From.Commit, err = r.Resolve(ctx, o.From); err != nil {
		return nil, fmt.Errorf("--from %q: %w", o.From, err)
	}
	if p.To.Commit, err = r.Resolve(ctx, o.To); err != nil {
		return nil, fmt.Errorf("--to %q: %w", o.To, err)
	}
	if p.MergeBase, err = r.MergeBase(ctx, p.From.Commit, p.To.Commit); err != nil {
		return nil, err
	}
	fromChanges, err := r.Changes(ctx, p.MergeBase, p.From.Commit)
	if err != nil {
		return nil, err
	}
	toChanges, err := r.Changes(ctx, p.MergeBase, p.To.Commit)
	if err != nil {
		return nil, err
	}
	var fromPaths []string
	changedFrom := map[string]bool{}
	for _, c := range fromChanges {
		fromPaths = append(fromPaths, c.Path)
		changedFrom[c.Path] = true
	}
	for _, c := range toChanges {
		if !changedFrom[c.Path] {
			p.ToOnly = append(p.ToOnly, c.Path)
		}
	}
	sort.Strings(p.ToOnly)
	differ, err := r.Differ(ctx, p.To.Commit, p.From.Commit, fromPaths)
	if err != nil {
		return nil, err
	}
	var candidates []gitrev.Change
	for _, c := range fromChanges {
		if differ[c.Path] {
			candidates = append(candidates, c)
		} else {
			p.Present++
		}
	}
	fmt.Fprintf(o.Log, "promote: %s → %s, merge base %s: %d file(s) to promote, %d already on %s, %d only on %s\n",
		o.From, o.To, short(p.MergeBase), len(candidates), p.Present, o.To, len(p.ToOnly), o.To)
	if len(candidates) == 0 {
		return p, nil
	}

	// the discovery maps of both revisions, exported into one private path
	if err := os.MkdirAll(o.Work, 0o700); err != nil {
		return nil, err
	}
	ws := filepath.Join(o.Work, "tree")
	maps := map[string]*discovery.Map{}
	for _, side := range []struct{ name, rev string }{{"to", p.To.Commit}, {"from", p.From.Commit}} {
		if err := r.Export(ctx, side.rev, ws); err != nil {
			return nil, err
		}
		m, err := review.DiscoverTree(ctx, ws, o.Entry, o.Helmfile)
		if err != nil {
			if side.name == "to" {
				return nil, fmt.Errorf("discovery on %s: %w", o.To, err)
			}
			p.Notes = append(p.Notes, fmt.Sprintf("no discovery map on %s (%v): new templates and deployments of %s are not known", o.From, err, o.From))
			m = maps["to"]
		}
		maps[side.name] = m
	}

	atoms := makeAtoms(candidates, maps["to"], maps["from"])
	skip := skipper(o.SkipClusters)
	toPairs := map[string]bool{}
	for _, pr := range maps["to"].Pairs {
		toPairs[pr.Key()] = true
	}
	for _, a := range atoms {
		res := classify.Classify(classify.Input{Base: maps["to"], PR: maps["from"], Entry: o.Entry, Changes: a.changes})
		if res.Mode == "full" {
			a.Full = res.Reason
			continue
		}
		var pairs []string
		for _, c := range res.Changes {
			pairs = append(pairs, c.Pairs...)
		}
		for _, k := range uniq(pairs) {
			cluster, _, _ := strings.Cut(k, "|")
			switch {
			case !toPairs[k]:
				a.NewPairs = append(a.NewPairs, k)
			case skip(cluster):
				a.SkippedPairs = append(a.SkippedPairs, k)
			default:
				a.Pairs = append(a.Pairs, k)
			}
		}
	}

	for _, group := range link(atoms, skip) {
		u, err := makeUnit(ctx, r, p, group)
		if err != nil {
			return nil, err
		}
		p.Units = append(p.Units, u)
	}
	if o.Split {
		if err := split(ctx, o, p, ws); err != nil {
			return nil, err
		}
	}
	for i := range p.Units {
		u := &p.Units[i]
		// no pair on To, or the render shows that none of them changes
		u.NoEffect = u.Full == "" && (len(u.Pairs) == 0 || u.Split != nil && u.Split.Changed != nil && len(u.Split.Changed) == 0)
		for _, k := range u.NewPairs {
			if cluster, _, _ := strings.Cut(k, "|"); !skip(cluster) {
				u.NoEffect = false
			}
		}
	}
	sortUnits(p.Units)
	return p, nil
}

// makeAtoms groups the changed files by the directory convention. The roots
// and the known templates and charts come from the discovery maps.
func makeAtoms(changes []gitrev.Change, maps ...*discovery.Map) []*Atom {
	D, T := "deployments", "templates"
	var templates, charts []string
	for _, m := range maps {
		if m == nil {
			continue
		}
		D, T = normRoot(m.DeploymentsRoot), normRoot(m.TemplatesRoot)
		for _, pr := range m.Pairs {
			templates = append(templates, pr.Templates...)
			charts = append(charts, pr.Charts...)
		}
	}
	templates, charts = uniq(templates), uniq(charts)
	byID := map[string]*Atom{}
	var order []string
	for _, c := range changes {
		id := atomID(c.Path, D, T, templates, charts)
		a := byID[id]
		if a == nil {
			a = &Atom{ID: id, Pairs: []string{}}
			byID[id] = a
			order = append(order, id)
		}
		a.Files = append(a.Files, c.Path)
		a.changes = append(a.changes, c)
	}
	sort.Strings(order)
	atoms := make([]*Atom, 0, len(order))
	for _, id := range order {
		atoms = append(atoms, byID[id])
	}
	return atoms
}

func atomID(p, D, T string, templates, charts []string) string {
	switch {
	case under(p, T):
		rel := strip(p, T)
		best := ""
		for _, t := range templates {
			if strings.HasPrefix(rel, t+"/") && len(t) > len(best) {
				best = t
			}
		}
		if best != "" {
			return "template:" + best
		}
		segs := strings.Split(rel, "/")
		if len(segs) == 1 {
			return "other:" + T
		}
		return "template:" + segs[0]
	case under(p, D):
		segs := strings.Split(strip(p, D), "/")
		for i, s := range segs {
			if s == "apps" && i+2 < len(segs) {
				return "deployment:" + strings.Join(segs[:i+2], "/")
			}
		}
		prefix := strings.Join(segs[:len(segs)-1], "/")
		if prefix == "" {
			return "hierarchy:global"
		}
		return "hierarchy:" + prefix
	}
	best := ""
	for _, ch := range charts {
		if strings.HasPrefix(p, ch+"/") && len(ch) > len(best) {
			best = ch
		}
	}
	if best != "" {
		return "chart:" + best
	}
	if top, _, ok := strings.Cut(p, "/"); ok {
		return "other:" + top
	}
	return "other:/"
}

// link groups atoms that reach a common pair on To, or a common new pair
// (one that To does not have yet) on a cluster deployed from To. A new pair
// on a skipped cluster links only what To needs to render it at all: the
// deployment that creates it and the atoms To does not have (a new template
// or chart); value effects there are not deployed from To, and the review of
// the promotion PR renders every pair. An atom that can change every pair
// stays alone: it would link everything; the plan marks it instead.
func link(atoms []*Atom, skip func(string) bool) [][]*Atom {
	parent := make([]int, len(atoms))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	owner := map[string]int{}
	for i, a := range atoms {
		if a.Full != "" {
			continue
		}
		keys := append([]string{}, a.Pairs...)
		for _, k := range a.NewPairs {
			cluster, dep, _ := strings.Cut(k, "|")
			if !skip(cluster) || a.added() || strings.HasPrefix(a.ID, "deployment:") && strings.HasSuffix(a.ID, "/apps/"+dep) {
				keys = append(keys, k)
			}
		}
		for _, k := range keys {
			if j, ok := owner[k]; ok {
				parent[find(i)] = find(j)
			} else {
				owner[k] = i
			}
		}
	}
	groups := map[int][]*Atom{}
	var roots []int
	for i, a := range atoms {
		root := find(i)
		if _, ok := groups[root]; !ok {
			roots = append(roots, root)
		}
		groups[root] = append(groups[root], a)
	}
	out := make([][]*Atom, 0, len(roots))
	for _, root := range roots {
		out = append(out, groups[root])
	}
	return out
}

// makeUnit builds a unit from linked atoms and checks its content against
// To: the unit's files applied on To with the merge base.
func makeUnit(ctx context.Context, r *gitrev.Repo, p *Plan, atoms []*Atom) (Unit, error) {
	u := Unit{Pairs: []string{}, Files: []gitrev.Change{}}
	var ids, paths, pairs, newPairs, skipped []string
	for _, a := range atoms {
		ids = append(ids, a.ID)
		u.Atoms = append(u.Atoms, *a)
		u.Files = append(u.Files, a.changes...)
		paths = append(paths, a.Files...)
		pairs = append(pairs, a.Pairs...)
		newPairs = append(newPairs, a.NewPairs...)
		skipped = append(skipped, a.SkippedPairs...)
		if a.Full != "" && u.Full == "" {
			u.Full = a.Full
		}
	}
	sort.Strings(ids)
	u.ID = strings.Join(ids, " + ")
	sort.Slice(u.Files, func(i, j int) bool { return u.Files[i].Path < u.Files[j].Path })
	u.Pairs, u.NewPairs, u.SkippedPairs = uniq(pairs), uniq(newPairs), uniq(skipped)
	status, conflicts, _, err := check(ctx, r, p, paths, u.ID)
	if err != nil {
		return u, err
	}
	u.Status, u.Conflicts = status, conflicts
	return u, nil
}

// check applies paths (as on From) on To with the merge base. It returns the
// status, the conflicting paths and the resulting commit-ish tree.
func check(ctx context.Context, r *gitrev.Repo, p *Plan, paths []string, label string) (string, []string, string, error) {
	commit, err := r.WithFiles(ctx, p.MergeBase, p.From.Commit, paths, "atlas promote: "+label)
	if err != nil {
		return "", nil, "", err
	}
	tree, conflicts, err := r.MergeTreeWithBase(ctx, p.MergeBase, p.To.Commit, commit)
	if err != nil {
		return "", nil, "", err
	}
	if len(conflicts) > 0 {
		return Conflicts, uniq(conflicts), "", nil
	}
	toTree, err := r.Tree(ctx, p.To.Commit)
	if err != nil {
		return "", nil, "", err
	}
	if tree == toTree {
		return Present, nil, tree, nil
	}
	return Applies, nil, tree, nil
}

// skipper matches a cluster against the skipped names: the full path or the
// leaf name.
func skipper(names []string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[strings.Trim(n, "/")] = true
	}
	return func(cluster string) bool {
		if set[cluster] {
			return true
		}
		leaf := cluster[strings.LastIndex(cluster, "/")+1:]
		return set[leaf]
	}
}

// sortUnits: applies first, then conflicts, then present; by id.
func sortUnits(units []Unit) {
	rank := map[string]int{Applies: 0, Conflicts: 1, Present: 2}
	sort.SliceStable(units, func(i, j int) bool {
		if rank[units[i].Status] != rank[units[j].Status] {
			return rank[units[i].Status] < rank[units[j].Status]
		}
		return units[i].ID < units[j].ID
	})
}

func normRoot(r string) string { return strings.TrimRight(strings.TrimPrefix(r, "./"), "/") }

func under(p, root string) bool { return root != "" && strings.HasPrefix(p, root+"/") }

func strip(p, root string) string { return p[len(root)+1:] }

func short(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

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
