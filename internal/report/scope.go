package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/snapshot"
)

// Shadow check results.
const (
	ShadowCovered      = "covered"      // every changed release is inside the selection
	ShadowUncovered    = "uncovered"    // a subset render would have missed changes
	ShadowFull         = "full"         // the classifier chose a full render: nothing to check
	ShadowSkipped      = "skipped"      // the render was a subset: no full diff to check against
	ShadowInconclusive = "inconclusive" // a side failed to render: partial trees judge nothing
)

// ShadowResult is the outcome of the shadow check.
type ShadowResult struct {
	Result   string
	Missed   []string // "<cluster>|<deployment>" outside the selection
	Markdown string   // shadow-check.md ("" when not written)
}

// Shadow asserts the property the subset cutover depends on: every release
// the FULL diff reports as changed belongs to a (cluster, deployment) pair
// the classifier selected. A miss is a false negative — a change a subset
// render would not have shown — and must surface while it is harmless.
func Shadow(affected []string, c classify.Result) ShadowResult {
	if c.Mode != "subset" {
		return ShadowResult{Result: ShadowFull, Markdown: "**Subset shadow check:** not applicable (classifier chose a full render).\n"}
	}
	selected := map[string]bool{}
	for _, s := range c.Selected {
		selected[s] = true
	}
	missedSet := map[string]bool{}
	for _, path := range affected {
		if strings.Count(path, "/") < 2 {
			continue
		}
		cluster, deployment, _ := snapshot.SplitRelease(path)
		if key := cluster + "|" + deployment; !selected[key] {
			missedSet[key] = true
		}
	}
	if len(missedSet) == 0 {
		return ShadowResult{Result: ShadowCovered,
			Markdown: fmt.Sprintf("**Subset shadow check:** every changed release is inside the classifier's selection (%d pairs selected).\n", len(c.Selected))}
	}
	missed := make([]string, 0, len(missedSet))
	for m := range missedSet {
		missed = append(missed, m)
	}
	sort.Strings(missed)
	var b strings.Builder
	fmt.Fprintf(&b, "> [!WARNING]\n> **Subset shadow check FAILED:** %d changed deployment(s) lie outside the classifier's selection. A subset render would have missed them — the full render above is authoritative. Please report this path/rule combination.\n>\n", len(missed))
	for _, m := range missed {
		cluster, deployment, _ := strings.Cut(m, "|")
		fmt.Fprintf(&b, "> - `%s` / `%s`\n", cluster, deployment)
	}
	return ShadowResult{Result: ShadowUncovered, Missed: missed, Markdown: b.String()}
}

// ScopeInput is what the render-scope fragment of the comment needs.
type ScopeInput struct {
	SubsetMode string // off / shadow / on (what the workflow ran with)
	RenderMode string // full / subset (what the render steps did)
	// Status of both renders: the shadow check judges complete renders only.
	PRStatus, BaselineStatus string
	Classify                 *classify.Result // nil = no classification
	// ClassifySummary is the classifier's comment fragment (classify-summary.md);
	// "" = derived from Classify.
	ClassifySummary string
	Affected        []string // changed release paths of the diff
}

// Scope builds the render-scope fragment and runs the shadow check:
//
//	off    → nothing
//	shadow → "full render" line + the classifier's would-be selection, plus
//	         the shadow check (every changed release inside the selection?)
//	on     → the classifier's scope line as is (it states a full render when
//	         it chose one)
//
// The returned shadow result is nil when no check applies (subsetting off).
func Scope(in ScopeInput) (string, *ShadowResult) {
	if in.SubsetMode == "off" || in.SubsetMode == "" || in.Classify == nil {
		return "", nil
	}
	c := *in.Classify
	summary := in.ClassifySummary
	if summary == "" {
		summary = classify.Summary(c)
	}
	var shadow ShadowResult
	switch {
	case in.PRStatus != "success" || in.BaselineStatus != "success":
		// a side that failed to render leaves a partial tree: the difference
		// of two partial trees says nothing about the classifier
		shadow = ShadowResult{Result: ShadowInconclusive,
			Markdown: "_Shadow check skipped: a side did not render completely, so the full diff cannot judge the selection._\n"}
	case in.RenderMode == "full":
		shadow = Shadow(in.Affected, c)
	default:
		shadow = ShadowResult{Result: ShadowSkipped}
	}
	var b strings.Builder
	if in.SubsetMode == "shadow" {
		b.WriteString("**Render scope:** full render (changed-file subsetting runs in shadow mode).\n")
		if c.Mode == "subset" {
			fmt.Fprintf(&b, "\n<details>\n<summary>Shadow: the classifier would have rendered %d of %d deployments</summary>\n\n", lenPtr(c.SelectedPR), c.PRTotal)
			b.WriteString(dropLines(summary, 2))
			b.WriteString("\n</details>\n")
		} else {
			fmt.Fprintf(&b, "\n_Shadow: the classifier would also have chosen a full render — %s._\n", c.Reason)
		}
	} else {
		b.WriteString(summary)
	}
	if strings.Contains(shadow.Markdown, "FAILED") || strings.Contains(shadow.Markdown, "skipped") {
		b.WriteString("\n" + shadow.Markdown)
	}
	return b.String(), &shadow
}

func lenPtr(s *[]string) int {
	if s == nil {
		return 0
	}
	return len(*s)
}

// dropLines removes the first n lines (sed '1,nd').
func dropLines(s string, n int) string {
	for i := 0; i < n; i++ {
		_, rest, ok := strings.Cut(s, "\n")
		if !ok {
			return ""
		}
		s = rest
	}
	return s
}
