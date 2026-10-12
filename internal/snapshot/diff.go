package snapshot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Diff modes.
const (
	ModeDyff    = "dyff"    // YAML- and Kubernetes-aware (homeport/dyff)
	ModeClassic = "classic" // unified diff
)

// DiffOptions describe one comparison of two render trees.
type DiffOptions struct {
	BaselineDir, PRDir string
	Mode               string // ModeDyff (default) or ModeClassic
	// SidedumpDir holds the PR side's redaction maps; a map of at most 3 bytes
	// ("{}") marks a release without secrets, which is safe to diff even when
	// the replay reported it as no-map.
	SidedumpDir string
	// NoMap lists the releases the replay found no map for (Scrub "no-map").
	NoMap map[string]bool
	// Parallel bounds the concurrent dyff/diff processes (default 4).
	Parallel int
}

// Diff results.
const (
	StatusEmpty     = "empty"      // nothing rendered on either side
	StatusNoChanges = "no-changes" // renders are equal
	StatusChanges   = "changes"
)

// ResourceDiff is one changed resource of a release.
type ResourceDiff struct {
	Label  string // Kind/name, or the file name
	Change string // "" (modified), "new" or "removed"
	Text   string // diff text, without trailing newline
}

// Lines is the number of lines of the diff text.
func (r ResourceDiff) Lines() int { return strings.Count(r.Text, "\n") + 1 }

// ReleaseDiff is one release that differs between the trees.
type ReleaseDiff struct {
	Path string // <cluster…>/<deployment>/<release>
	// Change: "" (on both sides), "new" (PR only) or "removed" (baseline only).
	Change string
	// Suppressed: "removed" or "no-map" — the release differs, but its
	// baseline may hold unredacted values, so no content is shown.
	Suppressed string
	Resources  []ResourceDiff
}

// Header is "<cluster> / <deployment> / <release>".
func (r ReleaseDiff) Header() string {
	c, d, n := SplitRelease(r.Path)
	return c + " / " + d + " / " + n
}

// DiffResult is the outcome of Diff.
type DiffResult struct {
	Status   string
	Releases []ReleaseDiff // changed and suppressed releases, by path
}

// Resources counts the changed resources (suppressed releases count none).
func (d DiffResult) Resources() int {
	n := 0
	for _, r := range d.Releases {
		n += len(r.Resources)
	}
	return n
}

// Suppressed counts suppressed releases with the given reason.
func (d DiffResult) Suppressed(reason string) int {
	n := 0
	for _, r := range d.Releases {
		if r.Suppressed == reason {
			n++
		}
	}
	return n
}

// Diff compares two render trees release by release and resource by
// resource. Both trees are split into one file per resource first (in place).
//
// Buckets:
//   - release only in the baseline → suppressed "removed": its content is
//     never shown, an old baseline may carry unredacted values;
//   - release on both sides without a usable redaction map → suppressed
//     "no-map", unless both sides are byte-identical (nothing can leak then);
//   - everything else → per-resource diffs; resources only on one side are
//     shown in full (new / removed).
func Diff(ctx context.Context, o DiffOptions) (DiffResult, error) {
	hasBase, hasPR := nonEmptyDir(o.BaselineDir), nonEmptyDir(o.PRDir)
	if !hasBase && !hasPR {
		return DiffResult{Status: StatusEmpty}, nil
	}
	noMap := map[string]bool{}
	for rel := range o.NoMap {
		if o.SidedumpDir != "" {
			if st, err := os.Stat(filepath.Join(o.SidedumpDir, filepath.FromSlash(rel)+".json")); err == nil && st.Size() <= 3 {
				continue // "{}": a current ATLAS saw no secrets in it
			}
		}
		noMap[rel] = true
	}
	baseReleases, err := Releases(o.BaselineDir)
	if err != nil {
		return DiffResult{}, err
	}
	prReleases, err := Releases(o.PRDir)
	if err != nil {
		return DiffResult{}, err
	}
	for _, root := range []string{o.BaselineDir, o.PRDir} {
		if err := Split(root); err != nil {
			return DiffResult{}, err
		}
	}
	inBase, inPR := set(baseReleases), set(prReleases)
	all := union(baseReleases, prReleases)

	parallel := o.Parallel
	if parallel < 1 {
		parallel = 4
	}
	d := differ{mode: o.Mode, sem: make(chan struct{}, parallel)}
	res := DiffResult{Status: StatusNoChanges}
	for _, rel := range all {
		baseDir := filepath.Join(o.BaselineDir, filepath.FromSlash(rel))
		prDir := filepath.Join(o.PRDir, filepath.FromSlash(rel))
		switch {
		case inBase[rel] && !inPR[rel]:
			res.Releases = append(res.Releases, ReleaseDiff{Path: rel, Change: "removed", Suppressed: "removed"})
			continue
		case inBase[rel] && inPR[rel] && noMap[rel]:
			same, err := sameTree(baseDir, prDir)
			if err != nil {
				return res, err
			}
			if !same {
				res.Releases = append(res.Releases, ReleaseDiff{Path: rel, Suppressed: "no-map"})
			}
			continue
		}
		resources, err := d.release(ctx, baseDir, prDir)
		if err != nil {
			return res, err
		}
		if len(resources) == 0 {
			continue
		}
		change := ""
		if !inBase[rel] {
			change = "new"
		}
		res.Releases = append(res.Releases, ReleaseDiff{Path: rel, Change: change, Resources: resources})
	}
	if len(res.Releases) > 0 {
		res.Status = StatusChanges
	}
	return res, nil
}

type differ struct {
	mode string
	sem  chan struct{}
}

// release diffs the resource files of one release (union of both sides).
func (d differ) release(ctx context.Context, baseDir, prDir string) ([]ResourceDiff, error) {
	baseFiles, err := ResourceFiles(baseDir)
	if err != nil {
		return nil, err
	}
	prFiles, err := ResourceFiles(prDir)
	if err != nil {
		return nil, err
	}
	inBase, inPR := set(baseFiles), set(prFiles)
	files := union(baseFiles, prFiles)
	out := make([]*ResourceDiff, len(files))
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	for i, f := range files {
		b, p := filepath.Join(baseDir, filepath.FromSlash(f)), filepath.Join(prDir, filepath.FromSlash(f))
		switch {
		case inBase[f] && inPR[f]:
			same, err := sameFile(b, p)
			if err != nil {
				return nil, err
			}
			if same {
				continue
			}
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				d.sem <- struct{}{}
				defer func() { <-d.sem }()
				text, err := d.pair(ctx, b, p)
				if err != nil {
					errs[i] = err
					return
				}
				if text != "" {
					out[i] = &ResourceDiff{Label: Label(p), Text: text}
				}
			}(i)
		case inPR[f]:
			text, err := d.whole(p, "added")
			if err != nil {
				return nil, err
			}
			out[i] = &ResourceDiff{Label: Label(p), Change: "new", Text: text}
		default:
			text, err := d.whole(b, "removed")
			if err != nil {
				return nil, err
			}
			out[i] = &ResourceDiff{Label: Label(b), Change: "removed", Text: text}
		}
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var res []ResourceDiff
	var resFiles []string
	for i, r := range out {
		if r != nil && r.Text != "" {
			res = append(res, *r)
			resFiles = append(resFiles, files[i])
		}
	}
	// Two resources of one kind and name (in different namespaces) carry the
	// same label: name the namespace so a reader can tell them apart.
	count := map[string]int{}
	for _, r := range res {
		count[r.Label]++
	}
	for i := range res {
		if count[res[i].Label] < 2 {
			continue
		}
		file := filepath.Join(prDir, filepath.FromSlash(resFiles[i]))
		if !inPR[resFiles[i]] {
			file = filepath.Join(baseDir, filepath.FromSlash(resFiles[i]))
		}
		if ns := namespace(file); ns != "" {
			res[i].Label += " in " + ns
		}
	}
	return res, nil
}

// namespace is metadata.namespace of the first resource in a file.
func namespace(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var n struct {
		Metadata struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if docs, err := documents(data); err == nil && len(docs) > 0 && yaml.Unmarshal(docs[0].text, &n) == nil {
		return n.Metadata.Namespace
	}
	return ""
}

// pair diffs one resource present on both sides; "" = no difference.
func (d differ) pair(ctx context.Context, base, pr string) (string, error) {
	var cmd *exec.Cmd
	if d.mode == ModeClassic {
		cmd = exec.CommandContext(ctx, "diff", "-u", "--label", "baseline", "--label", "current", base, pr)
	} else {
		cmd = exec.CommandContext(ctx, "dyff", "between", base, pr,
			"--output", "github", "--detect-kubernetes", "--omit-header", "--set-exit-code")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", err // the tool is missing — that is not "no difference"
	}
	// exit 1 = differences; any other failure: whatever it printed (as diff.sh)
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// whole renders a resource that exists on one side only, in full.
func (d differ) whole(file, direction string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	content := strings.TrimRight(string(data), "\n")
	sign := "+"
	if direction == "removed" {
		sign = "-"
	}
	var b strings.Builder
	if d.mode != ModeClassic {
		b.WriteString("! " + sign + " entire resource " + direction + ":\n")
	}
	for i, line := range strings.Split(content, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(sign + " " + line)
	}
	return b.String(), nil
}

// Label is "Kind/name" of the first resource in a file, else the file name
// without .yaml.
func Label(file string) string {
	data, err := os.ReadFile(file)
	if err == nil {
		if docs, err := documents(data); err == nil {
			for _, d := range docs {
				var n struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Name string `yaml:"name"`
					} `yaml:"metadata"`
				}
				if yaml.Unmarshal(d.text, &n) == nil && n.Kind != "" && n.Metadata.Name != "" {
					return n.Kind + "/" + n.Metadata.Name
				}
			}
		}
	}
	return strings.TrimSuffix(filepath.Base(file), ".yaml")
}

func nonEmptyDir(dir string) bool {
	if dir == "" {
		return false
	}
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

func sameFile(a, b string) (bool, error) {
	da, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(da, db), nil
}

// sameTree reports whether two directories hold the same files with the same bytes.
func sameTree(a, b string) (bool, error) {
	fa, err := allFiles(a)
	if err != nil {
		return false, err
	}
	fb, err := allFiles(b)
	if err != nil {
		return false, err
	}
	if len(fa) != len(fb) {
		return false, nil
	}
	for i := range fa {
		if fa[i] != fb[i] {
			return false, nil
		}
		same, err := sameFile(filepath.Join(a, fa[i]), filepath.Join(b, fb[i]))
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
}

func allFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

func union(a, b []string) []string {
	m := set(a)
	for _, x := range b {
		m[x] = true
	}
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
