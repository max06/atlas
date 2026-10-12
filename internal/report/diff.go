// Package report builds the review's markdown: the PR comment, the job
// summary and their fragments (per-release diff blocks, render scope, shadow
// check). The texts follow the former bash pipeline (diff.sh, comment.sh,
// shadow-check.sh) so existing comments read the same.
package report

import (
	"fmt"
	"strings"

	"github.com/max06/atlas/internal/snapshot"
)

// CommentLimit is the size (bytes) up to which the PR comment carries full
// release blocks; later releases appear as one-line "truncated" entries that
// point to the job summary. GitHub rejects comments above 65536 characters.
const CommentLimit = 55000

// DiffBlocks are the per-release markdown fragments of a diff.
type DiffBlocks struct {
	Comment   string // release blocks for the PR comment (truncated)
	Summary   string // release blocks for the job summary (complete)
	Affected  string // bullet list of affected releases
	Truncated bool
}

var suppressNotes = map[string]string{
	"removed": "release removed — diff suppressed to avoid leaking unredacted baseline output",
	"no-map":  "no PR-side redaction map — diff suppressed to avoid leaking template-level secrets from the pinned ATLAS version",
}

// Blocks renders the release sections: one outer <details> per release, one
// inner <details> per resource.
func Blocks(d snapshot.DiffResult) DiffBlocks {
	var comment, summary, affected strings.Builder
	truncated := false
	for _, r := range d.Releases {
		header := r.Header()
		if r.Suppressed != "" {
			block := fmt.Sprintf("\n<details>\n<summary>%s (%s)</summary>\n\n> %s\n\n</details>\n", header, r.Suppressed, suppressNotes[r.Suppressed])
			comment.WriteString(block)
			summary.WriteString(block)
			fmt.Fprintf(&affected, "\n- **%s** (%s)", header, r.Suppressed)
			continue
		}
		typeLabel := ""
		if r.Change != "" {
			typeLabel = " (" + r.Change + ")"
		}
		var resources strings.Builder
		for _, res := range r.Resources {
			resType := ""
			if res.Change != "" {
				resType = " (" + res.Change + ")"
			}
			fmt.Fprintf(&resources, "\n<details>\n<summary>%s%s (%d lines)</summary>\n\n```diff\n%s\n```\n\n</details>\n",
				res.Label, resType, res.Lines(), res.Text)
		}
		n := len(r.Resources)
		fmt.Fprintf(&affected, "\n- **%s**%s (%d resources)", header, typeLabel, n)
		block := fmt.Sprintf("\n<details>\n<summary>%s%s (%d resources)</summary>\n%s\n</details>\n", header, typeLabel, n, resources.String())
		summary.WriteString(block)
		if comment.Len()+len(block) > CommentLimit {
			truncated = true
			fmt.Fprintf(&comment, "\n<details>\n<summary>%s%s (%d resources, truncated)</summary>\n\n> Full diff available in the [job summary].\n\n</details>\n", header, typeLabel, n)
		} else {
			comment.WriteString(block)
		}
	}
	return DiffBlocks{Comment: comment.String(), Summary: summary.String(), Affected: affected.String(), Truncated: truncated}
}

// AffectedPaths lists every changed or suppressed release path, one per line.
func AffectedPaths(d snapshot.DiffResult) string {
	var b strings.Builder
	for _, r := range d.Releases {
		b.WriteString(r.Path + "\n")
	}
	return b.String()
}
