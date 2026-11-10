package ci

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/max06/atlas/internal/snapshot"
)

// ExportVersion is the version of the export layout (diff.json and the tree).
// Raise it when a field changes its meaning or disappears; new fields keep it.
const ExportVersion = 1

// ExportDiff is diff.json: the full diff of a review, never cut, for tools
// that read the review after it ran (for example an automated judgement).
// It holds exactly what the comment may show: suppressed releases carry no
// content, and the merge result's tree is redacted.
type ExportDiff struct {
	Version        int             `json:"version"`
	Base           string          `json:"base,omitempty"`
	Head           string          `json:"head,omitempty"`
	Status         string          `json:"status"`         // empty / no-changes / changes
	PRStatus       string          `json:"prStatus"`       // success / error
	BaselineStatus string          `json:"baselineStatus"` // success / error / missing
	RenderMode     string          `json:"renderMode"`     // full / subset
	Keyless        bool            `json:"keyless"`
	Pairs          []string        `json:"pairs,omitempty"` // "<cluster>|<deployment>" of both renders
	Releases       []ExportRelease `json:"releases"`
}

// ExportRelease is one changed or suppressed release.
type ExportRelease struct {
	Path       string           `json:"path"` // <cluster…>/<deployment>/<release>
	Cluster    string           `json:"cluster"`
	Deployment string           `json:"deployment"`
	Release    string           `json:"release"`
	Change     string           `json:"change,omitempty"`     // "" (both sides), new, removed
	Suppressed string           `json:"suppressed,omitempty"` // removed / no-map: no content
	Resources  []ExportResource `json:"resources"`
}

// ExportResource is one changed resource with its full diff text.
type ExportResource struct {
	Label  string `json:"label"`            // Kind/name, or the file name
	Change string `json:"change,omitempty"` // "" (modified), new, removed
	Diff   string `json:"diff"`
}

// ExportInput is what WriteExport needs from a finished review.
type ExportInput struct {
	Dir                      string // export directory (created, emptied first)
	PRDir                    string // the merge result's render tree (redacted)
	Base, Head               string
	Status                   string
	PRStatus, BaselineStatus string
	RenderMode               string
	Keyless                  bool
	Pairs                    []string
	Result                   snapshot.DiffResult
	// SkipTree: no merge-result/ copy (the tree is the big part of the export).
	SkipTree bool
	// Inventory, when set, goes to inventory.json.
	Inventory *Inventory
}

// WriteExport writes <Dir>/diff.json and copies the merge result's render
// tree to <Dir>/merge-result. The baseline's tree is never exported: an old
// baseline can hold values that only the replay over the PR side's maps
// redacts, and the export does not depend on that replay.
func WriteExport(in ExportInput, log io.Writer) error {
	if err := os.RemoveAll(in.Dir); err != nil {
		return err
	}
	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return err
	}
	d := ExportDiff{
		Version: ExportVersion, Base: in.Base, Head: in.Head,
		Status: firstNonEmpty(in.Status, in.Result.Status), PRStatus: in.PRStatus, BaselineStatus: in.BaselineStatus,
		RenderMode: firstNonEmpty(in.RenderMode, "full"), Keyless: in.Keyless, Pairs: in.Pairs,
		Releases: []ExportRelease{},
	}
	for _, r := range in.Result.Releases {
		c, dep, rel := snapshot.SplitRelease(r.Path)
		er := ExportRelease{Path: r.Path, Cluster: c, Deployment: dep, Release: rel,
			Change: r.Change, Suppressed: r.Suppressed, Resources: []ExportResource{}}
		if r.Suppressed == "" {
			for _, res := range r.Resources {
				er.Resources = append(er.Resources, ExportResource{Label: res.Label, Change: res.Change, Diff: res.Text})
			}
		}
		d.Releases = append(d.Releases, er)
	}
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(in.Dir, "diff.json"), append(body, '\n'), 0o644); err != nil {
		return err
	}
	if in.Inventory != nil {
		body, err := json.MarshalIndent(in.Inventory, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(in.Dir, "inventory.json"), append(body, '\n'), 0o644); err != nil {
			return err
		}
	}
	tree := filepath.Join(in.Dir, "merge-result")
	if !in.SkipTree && in.PRDir != "" && nonEmpty(in.PRDir) {
		if err := copyDir(in.PRDir, tree); err != nil {
			return fmt.Errorf("export the merge result's tree: %w", err)
		}
	}
	fmt.Fprintf(log, "export: %s (diff.json: %d release(s); inventory.json: %t; merge-result tree: %t)\n",
		in.Dir, len(d.Releases), in.Inventory != nil, nonEmpty(tree))
	return nil
}

func nonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// copyDir copies regular files and directories; symlinks and other file
// types are skipped (a render tree holds none, and following one could copy
// something from outside the tree).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case e.IsDir():
			return os.MkdirAll(target, 0o755)
		case e.Type().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		}
		return nil
	})
}
