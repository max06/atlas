// Package review compares two revisions of a consumer repository: which
// (cluster, deployment) pairs a change can affect, and what their renders
// look like on both sides.
package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gitrev"
	"github.com/max06/atlas/internal/helmfile"
)

// Spec says which revisions to compare.
type Spec struct {
	Base    string // target revision; "" = <remote>/<default branch>
	Head    string // "" = working tree (incl. uncommitted + untracked files)
	Remote  string // remote for the default base (default "origin")
	NoMerge bool   // compare Base and Head directly (CI: the platform already merged)
}

// Session holds the resolved revisions and the shared workspace.
type Session struct {
	Repo   *gitrev.Repo
	Entry  string // entry helmfile, repo-relative
	Binary string // helmfile binary ("" = helmfile)
	Log    io.Writer

	BaseRev, HeadRev string // commit ids as given/resolved
	HeadTree         string // tree reviewed as "after": merge result, or HeadRev with NoMerge
	// Workspace is the ONE directory both sides are exported into, one after
	// the other: rendered values embed the checkout path (atlas.cwd).
	Workspace string
}

// Open resolves the revisions of spec.
func Open(ctx context.Context, repo *gitrev.Repo, entry string, spec Spec, log io.Writer) (*Session, error) {
	s := &Session{Repo: repo, Entry: entry, Log: log}
	remote := spec.Remote
	if remote == "" {
		remote = "origin"
	}
	// The base is taken as it is in the clone, like `git diff origin/main`:
	// the CLI never fetches or writes refs. The log line carries the base's
	// commit date, so a stale local base is visible to the caller.
	base := spec.Base
	var err error
	if base == "" {
		if base, s.BaseRev, err = repo.DefaultBase(ctx, remote); err != nil {
			return nil, err
		}
	} else if s.BaseRev, err = repo.Resolve(ctx, base); err != nil {
		return nil, fmt.Errorf("base %q: %w", base, err)
	}
	baseLabel := fmt.Sprintf("%s (%s, %s)", base, short(s.BaseRev), repo.CommitDate(ctx, s.BaseRev))
	headLabel := spec.Head
	if spec.Head == "" {
		headLabel = "working tree"
		s.HeadRev, err = repo.Snapshot(ctx)
	} else {
		s.HeadRev, err = repo.Resolve(ctx, spec.Head)
	}
	if err != nil {
		return nil, fmt.Errorf("head %q: %w", headLabel, err)
	}
	if spec.NoMerge {
		s.HeadTree = s.HeadRev
		fmt.Fprintf(log, "review: base %s vs head %s (%s), no merge\n", baseLabel, headLabel, short(s.HeadRev))
	} else {
		if s.HeadTree, err = repo.MergeTree(ctx, s.BaseRev, s.HeadRev); err != nil {
			return nil, err
		}
		fmt.Fprintf(log, "review: base %s vs merge of %s (%s) into it\n", baseLabel, headLabel, short(s.HeadRev))
	}
	if s.Workspace, err = os.MkdirTemp("", "atlas-review-"); err != nil {
		return nil, err
	}
	s.Workspace = filepath.Join(s.Workspace, "tree")
	return s, nil
}

// Close removes the workspace.
func (s *Session) Close() { os.RemoveAll(filepath.Dir(s.Workspace)) }

func short(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// Side names one revision of the review.
type Side string

const (
	Base Side = "base"
	Head Side = "head"
)

func (s *Session) rev(side Side) string {
	if side == Base {
		return s.BaseRev
	}
	return s.HeadTree
}

// checkout exports one side into the shared workspace.
func (s *Session) checkout(ctx context.Context, side Side) error {
	return s.Repo.Export(ctx, s.rev(side), s.Workspace)
}

// runner returns a helmfile runner for the workspace whose stderr (progress
// chatter of helm/helmfile) is captured; withLog attaches its tail to errors.
func (s *Session) runner() (helmfile.Runner, *bytes.Buffer) {
	var buf bytes.Buffer
	return helmfile.Runner{Binary: s.Binary, File: s.Entry, Dir: s.Workspace, Stderr: &buf}, &buf
}

func withLog(err error, log *bytes.Buffer) error {
	if err == nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return fmt.Errorf("%w\n  %s", err, strings.Join(lines, "\n  "))
}

// ErrNoEntry means a side has no entry helmfile (the repo predates ATLAS there).
var ErrNoEntry = errors.New("entry helmfile missing")

// Classification is a classify result plus both maps (nil = side has none).
type Classification struct {
	classify.Result
	BaseMap, HeadMap *discovery.Map
	// BaseErr says why BaseMap is nil: ErrNoEntry, discovery.ErrUnsupported
	// (ATLAS without map mode), or a discovery failure (the side is broken).
	BaseErr error
}

// BaseBroken reports whether the target side failed discovery for another
// reason than "no entry" or "no map mode" — its render fails the same way.
func (c *Classification) BaseBroken() bool {
	return c.BaseMap == nil && c.BaseErr != nil &&
		!errors.Is(c.BaseErr, ErrNoEntry) && !errors.Is(c.BaseErr, discovery.ErrUnsupported)
}

// Classify discovers both sides and classifies the changed paths.
func (s *Session) Classify(ctx context.Context) (*Classification, error) {
	changes, err := s.Repo.Changes(ctx, s.BaseRev, s.HeadTree)
	if err != nil {
		return nil, err
	}
	out := &Classification{}
	for _, side := range []Side{Base, Head} {
		m, err := s.discover(ctx, side)
		if err != nil {
			if side == Head {
				return nil, fmt.Errorf("discovery on the %s side: %w", side, err)
			}
			// the base may predate ATLAS or be broken — the change may be the fix
			fmt.Fprintf(s.Log, "review: no discovery map on the %s side: %v\n", side, err)
		}
		if side == Base {
			out.BaseMap, out.BaseErr = m, err
		} else {
			out.HeadMap = m
		}
	}
	out.Result = classify.Classify(classify.Input{Base: out.BaseMap, PR: out.HeadMap, Entry: s.Entry, Changes: changes})
	return out, nil
}

func (s *Session) discover(ctx context.Context, side Side) (*discovery.Map, error) {
	if err := s.checkout(ctx, side); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(s.Workspace, s.Entry)); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Entry, ErrNoEntry)
	}
	r, log := s.runner()
	m, err := discovery.Discover(ctx, r)
	if err != nil {
		return nil, withLog(err, log)
	}
	if m.Version < discovery.MinVersion {
		fmt.Fprintf(s.Log, "review: %s side has discovery map v%d (< v%d): chart edges unknown\n", side, m.Version, discovery.MinVersion)
	}
	return m, nil
}

// RenderOptions control the renders of a review.
type RenderOptions struct {
	SkipSecrets, Redact bool
	// BaseErr: the target side is known broken (failed discovery) — it is not
	// rendered, Render reports it as a base error.
	BaseErr  error
	Parallel int // concurrent helmfile invocations (one per cluster)
}

// Render status of one side (the render.sh outputs of the review pipeline).
const (
	StatusSuccess = "success"
	StatusError   = "error"   // render failed — the side's tree is incomplete
	StatusMissing = "missing" // no entry helmfile on that side (repo predates ATLAS)
)

// RenderResult says how each side rendered. A base error is not fatal for
// Render (the change may be the fix); the caller decides, because a diff
// against a failed base shows every release of the selection as added.
type RenderResult struct {
	Base, Head string
	BaseErr    error
}

// Render renders both sides into outDir/base and outDir/head. With a subset
// classification only the selected pairs render; otherwise everything.
func (s *Session) Render(ctx context.Context, c *Classification, outDir string, opts RenderOptions) (RenderResult, error) {
	res := RenderResult{Base: StatusSuccess, Head: StatusSuccess}
	for _, side := range []Side{Base, Head} {
		dir := filepath.Join(outDir, string(side))
		if err := os.RemoveAll(dir); err != nil {
			return res, err
		}
		if side == Base && opts.BaseErr != nil {
			res.Base, res.BaseErr = StatusError, opts.BaseErr
			continue
		}
		if err := s.checkout(ctx, side); err != nil {
			return res, err
		}
		if _, err := os.Stat(filepath.Join(s.Workspace, s.Entry)); err != nil {
			if side == Base {
				fmt.Fprintf(s.Log, "review: %s side has no %s, nothing to render\n", side, s.Entry)
				res.Base = StatusMissing
				continue
			}
			return res, fmt.Errorf("%s side: entry helmfile %s missing", side, s.Entry)
		}
		var pairs *[]string
		if c != nil && c.Mode == "subset" {
			pairs = c.SelectedPR
			if side == Base {
				pairs = c.SelectedBaseline
			}
		}
		err := s.renderSide(ctx, dir, pairs, opts)
		if err != nil && side == Base {
			res.Base, res.BaseErr = StatusError, err
			continue
		}
		if err != nil {
			res.Head = StatusError
			return res, fmt.Errorf("%s render: %w", side, err)
		}
	}
	return res, nil
}

// renderSide renders the workspace into dir: everything when pairs is nil,
// else one helmfile invocation per cluster with that cluster's deployments
// (stage-1 filter) plus per-pair selectors as the release-level safety net.
func (s *Session) renderSide(ctx context.Context, dir string, pairs *[]string, opts RenderOptions) error {
	base := helmfile.Options{SkipSecrets: opts.SkipSecrets, Redact: opts.Redact}
	args := append(helmfile.TemplateArgs(), "--output-dir", dir, "--output-dir-template", helmfile.OutputDirTemplate)
	if pairs == nil {
		r, log := s.runner()
		return withLog(r.Run(ctx, base, io.Discard, args...), log)
	}
	byCluster := map[string][]string{}
	for _, k := range *pairs {
		cluster, deployment, _ := strings.Cut(k, "|")
		byCluster[cluster] = append(byCluster[cluster], deployment)
	}
	clusters := make([]string, 0, len(byCluster))
	for c := range byCluster {
		clusters = append(clusters, c)
	}
	sort.Strings(clusters)
	fmt.Fprintf(s.Log, "review: rendering %d pair(s) in %d cluster(s)\n", len(*pairs), len(clusters))

	parallel := opts.Parallel
	if parallel < 1 {
		parallel = 4
	}
	sem := make(chan struct{}, parallel)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster string, deployments []string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			o := base
			o.Clusters, o.Deployments = []string{cluster}, deployments
			a := append([]string{}, args...)
			for _, d := range deployments {
				a = append(a, "--selector", "cluster="+cluster+",deploymentName="+d)
			}
			r, log := s.runner()
			if err := withLog(r.Run(ctx, o, io.Discard, a...), log); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s [%s]: %w", cluster, strings.Join(deployments, ","), err))
				mu.Unlock()
			}
		}(cluster, byCluster[cluster])
	}
	wg.Wait()
	return errors.Join(errs...)
}
