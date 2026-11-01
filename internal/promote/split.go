package promote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/max06/atlas/internal/review"
)

// Split is the render evidence of a unit that went through the split.
type Split struct {
	// Unit is the id of the linked unit before the split.
	Unit string `json:"unit"`
	// Pairs rendered on To (with all atoms, with each atom alone, without).
	Pairs []string `json:"pairs"`
	// Releases each atom changes on its own.
	Releases map[string][]string `json:"releases"`
	// Links say why atoms stay together.
	Links []string `json:"links"`
	// Changed are the releases the unit changes on To; nil when the renders
	// did not tell (a side did not render).
	Changed []string `json:"changed"`
}

// split separates the atoms of each applying unit with more than one atom.
// The unit's pairs on To render without the unit, with all its atoms and
// with each atom alone, from one workspace path (rendered values embed it).
// Atoms stay linked when they change the same release, when a release
// renders differently with all atoms than with the one atom that changes
// it, or when an atom does not render alone. Atoms linked through a new
// pair (a new deployment and its template) stay together: To cannot render
// that pair without them.
func split(ctx context.Context, o Options, p *Plan, ws string) error {
	var out []Unit
	for _, u := range p.Units {
		if u.Status != Applies || u.Full != "" || len(u.Pairs) == 0 {
			out = append(out, u)
			continue
		}
		parts, err := splitUnit(ctx, o, p, ws, u)
		if err != nil {
			return err
		}
		out = append(out, parts...)
	}
	p.Units = out
	return nil
}

func splitUnit(ctx context.Context, o Options, p *Plan, ws string, u Unit) ([]Unit, error) {
	r := o.Repo
	n := len(u.Atoms)
	fmt.Fprintf(o.Log, "promote: render %s (%d atom(s), %d pair(s))\n", u.ID, n, len(u.Pairs))
	renders := filepath.Join(o.Work, "renders")
	if err := os.MkdirAll(renders, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(renders)
	render := func(name, rev string) (string, error) {
		if err := r.Export(ctx, rev, ws); err != nil {
			return "", err
		}
		dir := filepath.Join(renders, name)
		err := review.RenderTree(ctx, ws, o.Entry, o.Helmfile, u.Pairs, dir, review.RenderOptions{SkipSecrets: true}, o.Log)
		return dir, err
	}
	sp := &Split{Unit: u.ID, Pairs: u.Pairs, Releases: map[string][]string{}, Links: []string{}}
	keep := func(why string) ([]Unit, error) {
		sp.Links = append(sp.Links, why)
		u.Split = sp
		return []Unit{u}, nil
	}
	base, err := render("to", p.To.Commit)
	if err != nil {
		return keep(fmt.Sprintf("no split: %s does not render (%v)", p.To.Ref, firstLine(err)))
	}
	var paths []string
	for _, a := range u.Atoms {
		paths = append(paths, a.Files...)
	}
	_, _, allTree, err := check(ctx, r, p, paths, u.ID)
	if err != nil {
		return nil, err
	}
	all, err := render("all", allTree)
	if err != nil {
		return keep(fmt.Sprintf("no split: %s with all atoms does not render (%v)", p.To.Ref, firstLine(err)))
	}
	changedAll, err := changed(base, all, u.Pairs)
	if err != nil {
		return nil, err
	}
	sp.Changed = sortedSet(changedAll)
	if n == 1 {
		sp.Releases[u.Atoms[0].ID] = sp.Changed
		u.Split = sp
		return []Unit{u}, nil
	}

	parent := make([]int, n)
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
	union := func(i, j int, why string) {
		if find(i) != find(j) {
			parent[find(i)] = find(j)
			sp.Links = append(sp.Links, why)
		}
	}
	// atoms on a common new pair stay together
	newOwner := map[string]int{}
	for i, a := range u.Atoms {
		for _, k := range a.NewPairs {
			if j, ok := newOwner[k]; ok {
				union(i, j, fmt.Sprintf("%s + %s: new deployment %s", u.Atoms[j].ID, a.ID, k))
			} else {
				newOwner[k] = i
			}
		}
	}
	touches := func(pair string) []int {
		var out []int
		for i, a := range u.Atoms {
			for _, k := range a.Pairs {
				if k == pair {
					out = append(out, i)
					break
				}
			}
		}
		return out
	}

	alone := make([]map[string]bool, n) // releases atom i changes on its own
	diffAll := make([]map[string]bool, n)
	for i, a := range u.Atoms {
		status, _, tree, err := check(ctx, r, p, a.Files, a.ID)
		if err != nil {
			return nil, err
		}
		if status == Conflicts {
			for j := range u.Atoms {
				union(i, j, fmt.Sprintf("%s: conflicts on %s alone", a.ID, p.To.Ref))
			}
			continue
		}
		if status == Present {
			alone[i], diffAll[i] = map[string]bool{}, map[string]bool{}
			continue
		}
		dir, err := render(fmt.Sprintf("atom-%d", i), tree)
		if err != nil {
			for j := range u.Atoms {
				union(i, j, fmt.Sprintf("%s: does not render alone (%s)", a.ID, firstLine(err)))
			}
			continue
		}
		if alone[i], err = changed(base, dir, u.Pairs); err != nil {
			return nil, err
		}
		if diffAll[i], err = changed(dir, all, u.Pairs); err != nil {
			return nil, err
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		rel := []string{}
		for k := range alone[i] {
			rel = append(rel, k)
		}
		sort.Strings(rel)
		sp.Releases[a.ID] = rel
	}

	releases := map[string]bool{}
	for k := range changedAll {
		releases[k] = true
	}
	for _, m := range alone {
		for k := range m {
			releases[k] = true
		}
	}
	for _, rel := range sortedSet(releases) {
		var owners []int
		for i := range u.Atoms {
			if alone[i][rel] {
				owners = append(owners, i)
			}
		}
		switch {
		case len(owners) > 1:
			for _, j := range owners[1:] {
				union(owners[0], j, fmt.Sprintf("%s + %s: both change %s", u.Atoms[owners[0]].ID, u.Atoms[j].ID, rel))
			}
		case len(owners) == 1:
			i := owners[0]
			if diffAll[i] != nil && diffAll[i][rel] {
				for _, j := range touches(pairOf(rel)) {
					union(i, j, fmt.Sprintf("%s + %s: %s renders differently together", u.Atoms[i].ID, u.Atoms[j].ID, rel))
				}
			}
		default:
			// changes only in combination
			ts := touches(pairOf(rel))
			for _, j := range ts {
				union(ts[0], j, fmt.Sprintf("%s + %s: %s changes only together", u.Atoms[ts[0]].ID, u.Atoms[j].ID, rel))
			}
		}
	}

	groups := map[int][]*Atom{}
	var roots []int
	for i := range u.Atoms {
		root := find(i)
		if _, ok := groups[root]; !ok {
			roots = append(roots, root)
		}
		a := u.Atoms[i]
		groups[root] = append(groups[root], &a)
	}
	if len(roots) == 1 {
		u.Split = sp
		return []Unit{u}, nil
	}
	fmt.Fprintf(o.Log, "promote: %s splits into %d units\n", u.ID, len(roots))
	var out []Unit
	for _, root := range roots {
		part, err := makeUnit(ctx, r, p, groups[root])
		if err != nil {
			return nil, err
		}
		own := &Split{Unit: u.ID, Pairs: u.Pairs, Releases: map[string][]string{}, Links: []string{}}
		for _, a := range part.Atoms {
			if rel, ok := sp.Releases[a.ID]; ok {
				own.Releases[a.ID] = rel
			}
			for _, l := range sp.Links {
				if strings.Contains(l, a.ID) && !contains(own.Links, l) {
					own.Links = append(own.Links, l)
				}
			}
		}
		changedPart := map[string]bool{}
		for _, a := range part.Atoms {
			for _, rel := range sp.Releases[a.ID] {
				changedPart[rel] = true
			}
		}
		own.Changed = sortedSet(changedPart)
		part.Split = own
		out = append(out, part)
	}
	return out, nil
}

// changed returns the releases ("<cluster>/<deployment>/<release>") whose
// rendered files differ between two render trees.
func changed(a, b string, pairs []string) (map[string]bool, error) {
	list, err := review.Compare(a, b, pairs)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range list {
		out[c.Release] = true
	}
	return out, nil
}

// pairOf: "<cluster>/<deployment>/<release>" → "<cluster>|<deployment>".
func pairOf(release string) string {
	segs := strings.Split(release, "/")
	if len(segs) < 3 {
		return release
	}
	return strings.Join(segs[:len(segs)-2], "/") + "|" + segs[len(segs)-2]
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
