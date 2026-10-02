package classify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFiles writes the classify.sh output set into dir: classify.json,
// pairs-baseline.txt, pairs-pr.txt and classify-summary.md. When githubOutput
// is set, the step outputs are appended to it as classify.sh does.
func WriteFiles(dir string, r Result, githubOutput string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "classify.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	var base, pr []string
	if r.Mode == "subset" {
		base, pr = *r.SelectedBaseline, *r.SelectedPR
	}
	if err := writeLines(filepath.Join(dir, "pairs-baseline.txt"), base); err != nil {
		return err
	}
	if err := writeLines(filepath.Join(dir, "pairs-pr.txt"), pr); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "classify-summary.md"), []byte(Summary(r)), 0o644); err != nil {
		return err
	}
	if githubOutput == "" {
		return nil
	}
	f, err := os.OpenFile(githubOutput, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "mode=%s\nreason=%s\nselected_baseline=%d\ntotal_baseline=%d\nselected_pr=%d\ntotal_pr=%d\npairs_baseline_file=%s\npairs_pr_file=%s\n",
		r.Mode, r.Reason, len(base), r.BaseTotal, len(pr), r.PRTotal,
		filepath.Join(dir, "pairs-baseline.txt"), filepath.Join(dir, "pairs-pr.txt"))
	return err
}

func writeLines(path string, lines []string) error {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// Summary is the markdown fragment for the review comment.
func Summary(r Result) string {
	var b strings.Builder
	if r.Mode != "subset" {
		fmt.Fprintf(&b, "**Render scope:** full render — %s.\n", r.Reason)
		return b.String()
	}
	fmt.Fprintf(&b, "**Render scope:** %d of %d deployments (merge result), %d of %d (target branch) — selected from the changed paths.\n\n",
		len(*r.SelectedPR), r.PRTotal, len(*r.SelectedBaseline), r.BaseTotal)
	b.WriteString("<details>\n<summary>Selection rules applied</summary>\n\n| Changed path | Rule | Selected |\n|---|---|---|\n")
	for _, c := range r.Changes {
		detail := ""
		if c.Detail != "" {
			detail = "`" + c.Detail + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s %s | %d |\n", c.Path, c.Rule, detail, len(c.Pairs))
	}
	if n := len(*r.OneSideOnly); n > 0 {
		fmt.Fprintf(&b, "| _(pairs present on one revision only)_ | added/removed | %d |\n", n)
	}
	b.WriteString("\n</details>\n")
	return b.String()
}
