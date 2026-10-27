package promote

import (
	"fmt"
	"strings"
)

// Text is the plan for a terminal.
func Text(p *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s → %s (merge base %s): %d unit(s)", p.From.Ref, p.To.Ref, short(p.MergeBase), len(p.Units))
	fmt.Fprintf(&b, ", %d file(s) already on %s, %d file(s) only on %s\n", p.Present, p.To.Ref, len(p.ToOnly), p.To.Ref)
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "note: %s\n", n)
	}
	for _, u := range p.Units {
		fmt.Fprintf(&b, "\n%-9s %s\n", u.Status, u.ID)
		for _, f := range u.Files {
			fmt.Fprintf(&b, "          %s %s\n", f.Status, f.Path)
		}
		if len(u.Conflicts) > 0 {
			fmt.Fprintf(&b, "          conflicts: %s\n", strings.Join(u.Conflicts, ", "))
		}
		fmt.Fprintf(&b, "          %s\n", effect(u, p.To.Ref))
		if u.Split != nil {
			for _, l := range u.Split.Links {
				fmt.Fprintf(&b, "          linked: %s\n", l)
			}
		}
	}
	return b.String()
}

// effect is one line: what the unit changes on To.
func effect(u Unit, to string) string {
	var parts []string
	switch {
	case u.Full != "":
		parts = append(parts, "can change every deployment ("+u.Full+")")
	case len(u.Pairs) == 0:
		parts = append(parts, "no deployment on "+to)
	case u.Split != nil && u.Split.Changed != nil && len(u.Split.Changed) == 0:
		parts = append(parts, fmt.Sprintf("renders unchanged on %s (%d deployment(s): %s)", to, len(u.Pairs), list(u.Pairs, 6)))
	case u.Split != nil && u.Split.Changed != nil:
		parts = append(parts, fmt.Sprintf("%d release(s) change on %s: %s", len(u.Split.Changed), to, list(u.Split.Changed, 6)))
	default:
		parts = append(parts, fmt.Sprintf("%d deployment(s) on %s: %s", len(u.Pairs), to, list(u.Pairs, 6)))
	}
	if len(u.NewPairs) > 0 {
		parts = append(parts, fmt.Sprintf("new: %s", list(u.NewPairs, 6)))
	}
	if len(u.SkippedPairs) > 0 {
		parts = append(parts, fmt.Sprintf("%d on skipped clusters", len(u.SkippedPairs)))
	}
	if u.Split != nil && u.Split.Unit != u.ID {
		parts = append(parts, "split from "+u.Split.Unit)
	}
	return strings.Join(parts, "; ")
}

func list(items []string, n int) string {
	shown := make([]string, 0, n)
	for i, s := range items {
		if i == n {
			break
		}
		shown = append(shown, strings.Replace(s, "|", "/", 1))
	}
	out := strings.Join(shown, ", ")
	if len(items) > n {
		out += fmt.Sprintf(" and %d more", len(items)-n)
	}
	return out
}

// Markdown is the body of the promote dashboard: one task list item per
// unit that applies (the checkbox starts the carry-over), conflicts and
// present units without a checkbox. Each item carries the unit id in an
// HTML comment for the workflow that reads the ticked boxes.
func Markdown(p *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Promote `%s` → `%s`\n\n", p.From.Ref, p.To.Ref)
	counts := map[string]int{}
	for _, u := range p.Units {
		counts[u.Status]++
	}
	fmt.Fprintf(&b, "Merge base `%s`. %d unit(s): %d apply, %d conflict. %d changed file(s) are already on `%s`; %d file(s) changed only on `%s`.\n\n",
		short(p.MergeBase), len(p.Units), counts[Applies], counts[Conflicts], p.Present, p.To.Ref, len(p.ToOnly), p.To.Ref)
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "> %s\n\n", n)
	}
	section := func(status string, noEffect bool, title string) {
		var units []Unit
		for _, u := range p.Units {
			if u.Status == status && (status != Applies || u.NoEffect == noEffect) {
				units = append(units, u)
			}
		}
		if len(units) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s\n\n", title)
		for _, u := range units {
			box := "- "
			if status == Applies {
				box = "- [ ] "
			}
			fmt.Fprintf(&b, "%s**%s** — %d file(s); %s <!-- atlas-promote-unit: %s -->\n", box, u.ID, len(u.Files), effect(u, "`"+p.To.Ref+"`"), u.ID)
			fmt.Fprintf(&b, "  <details><summary>files</summary>\n\n")
			for _, f := range u.Files {
				fmt.Fprintf(&b, "  - `%s` %s\n", f.Path, f.Status)
			}
			fmt.Fprintf(&b, "\n  </details>\n")
			if len(u.Conflicts) > 0 {
				fmt.Fprintf(&b, "  - conflicts in: %s\n", code(u.Conflicts))
			}
			if u.Split != nil && len(u.Split.Links) > 0 {
				fmt.Fprintf(&b, "  - linked: %s\n", strings.Join(u.Split.Links, "; "))
			}
		}
		b.WriteString("\n")
	}
	section(Applies, false, "Ready: changes deployments on `"+p.To.Ref+"`")
	section(Applies, true, "Ready: no effect on deployments of `"+p.To.Ref+"`")
	section(Conflicts, false, "Conflicts (the branches diverged: resolve on the branches, then the unit shows here again)")
	section(Present, false, "Present")
	return b.String()
}

func code(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = "`" + s + "`"
	}
	return strings.Join(q, ", ")
}
