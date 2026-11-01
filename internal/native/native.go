// Package native renders one side of a review with the native engine: the
// states from internal/engine, the manifests from internal/renderer, the
// redaction maps from a generation in ATLAS_REDACT_MAPS mode. The CI render
// step and the local review both use it, so the two render a native tree the
// same way.
package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/max06/atlas/internal/config"
	"github.com/max06/atlas/internal/engine"
	"github.com/max06/atlas/internal/helmfile"
	"github.com/max06/atlas/internal/renderer"
	"github.com/max06/atlas/internal/snapshot"
)

// Layout tells whether the tree in dir renders with the native engine
// (atlas.yaml spec.native) and returns its layout. Without atlas.yaml, or
// without the switch, the tree renders with the helmfile templates.
func Layout(dir string) (engine.Layout, bool, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return engine.Layout{}, false, err
	}
	r, err := config.Load(root)
	if errors.Is(err, config.ErrNotFound) {
		return engine.Layout{}, false, nil
	}
	if err != nil || !r.Spec.Native {
		return engine.Layout{}, false, err
	}
	return engine.Layout{Root: root, Templates: r.Spec.TemplateDir, Deployments: r.Spec.DeploymentDir}, true, nil
}

// Options of a side render.
type Options struct {
	Layout engine.Layout
	// Pairs ("<cluster>|<deployment>") to render; nil: every pair of the tree.
	Pairs []string
	// OutputDir gets <cluster>/<deployment>/<release>/... (the review layout).
	OutputDir   string
	SkipSecrets bool
	// MapsDir: write the redaction maps of the pairs here (the sidedump
	// layout); "" for none.
	MapsDir string
	// Work is a private directory for the generated states (they hold
	// decrypted values); it is created and removed.
	Work string
	// Log gets the renderer's progress and every error.
	Log io.Writer
	// Phase, when set, is called at the start of each phase.
	Phase func(string)
}

// Result of a side render.
type Result struct {
	Pairs []string
	// Failed are the pairs without (complete) output, with their errors: the
	// states or the maps failed, or a release did not render. Nothing of a
	// failed pair is left in OutputDir.
	Failed map[string][]error
}

// Err joins the errors of the failed pairs (nil when none failed).
func (r Result) Err() error {
	var errs []error
	for _, p := range r.FailedPairs() {
		errs = append(errs, r.Failed[p]...)
	}
	return errors.Join(errs...)
}

// FailedPairs are the failed pairs, sorted.
func (r Result) FailedPairs() []string {
	keys := make([]string, 0, len(r.Failed))
	for k := range r.Failed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Render renders the pairs of a side. A failed pair or release does not stop
// the others (helmfile's --allow-failed-releases); err is for everything
// that is not one pair's failure.
func Render(ctx context.Context, o Options) (Result, error) {
	res := Result{Failed: map[string][]error{}}
	if o.Log == nil {
		o.Log = io.Discard
	}
	phase := func(p string) {
		if o.Phase != nil {
			o.Phase(p)
		}
	}
	pairs := o.Pairs
	if pairs == nil {
		phase("discovery (native engine)")
		found, err := engine.Discover(o.Layout, engine.Filter{})
		if err != nil {
			return res, err
		}
		seen := map[string]bool{}
		for _, p := range found {
			k := p.Cluster + "|" + p.DeploymentName
			if !seen[k] {
				seen[k] = true
				pairs = append(pairs, k)
			}
		}
		sort.Strings(pairs)
	}
	res.Pairs = pairs
	if err := os.MkdirAll(o.OutputDir, 0o755); err != nil {
		return res, err
	}
	if len(pairs) == 0 {
		return res, nil
	}
	if err := os.RemoveAll(o.Work); err != nil {
		return res, err
	}
	if err := os.MkdirAll(o.Work, 0o700); err != nil {
		return res, err
	}
	defer os.RemoveAll(o.Work)
	fail := func(pair string, err error) {
		cluster, dep, _ := strings.Cut(pair, "|")
		fmt.Fprintf(o.Log, "%s/%s: %v\n", cluster, dep, err)
		res.Failed[pair] = append(res.Failed[pair], err)
	}

	if o.MapsDir != "" {
		phase(fmt.Sprintf("redaction maps (native engine, %d pair(s))", len(pairs)))
		eng, err := engine.New(o.Layout, engine.Switches{RedactMaps: true})
		if err != nil {
			return res, err
		}
		root, genErrs, err := generate(eng, filepath.Join(o.Work, "maps"), pairs)
		if err != nil {
			return res, err
		}
		for _, p := range sortedKeys(genErrs) {
			fail(p, fmt.Errorf("redaction maps: %w", genErrs[p]))
		}
		if root != nil {
			n, err := snapshot.WriteMaps(o.MapsDir, root.RedactMaps())
			if err != nil {
				return res, err
			}
			fmt.Fprintf(o.Log, "redaction maps: %d release(s)\n", n)
		}
	}

	phase(fmt.Sprintf("native engine: states of %d pair(s)", len(pairs)))
	eng, err := engine.New(o.Layout, engine.Switches{SkipSecrets: o.SkipSecrets})
	if err != nil {
		return res, err
	}
	states, genErrs, err := generate(eng, filepath.Join(o.Work, "states"), pairs)
	if err != nil {
		return res, err
	}
	for _, p := range sortedKeys(genErrs) {
		fail(p, genErrs[p])
	}
	if states != nil && len(states.Pairs()) > 0 {
		phase(fmt.Sprintf("native render of %d pair(s)", len(pairs)))
		ro := renderer.Options{
			OutputDir: o.OutputDir, OutputDirTemplate: helmfile.OutputDirTemplate,
			Stdout: io.Discard, Log: o.Log, AllowFailedReleases: true,
		}
		// the generation may cover more than the pairs (the cross product
		// of the filter): selectors keep exactly the pairs
		for _, p := range pairs {
			cluster, dep, _ := strings.Cut(p, "|")
			sel, err := renderer.ParseSelector("cluster=" + cluster + ",deploymentName=" + dep)
			if err != nil {
				return res, err
			}
			ro.Selectors = append(ro.Selectors, sel)
		}
		if err := renderer.Render(ctx, states, ro); err != nil {
			list := []error{err}
			var joined interface{ Unwrap() []error }
			if errors.As(err, &joined) {
				list = joined.Unwrap()
			}
			for _, e := range list {
				var re *renderer.ReleaseError
				if !errors.As(e, &re) {
					return res, err // not one release: the whole render
				}
				cluster, dep := re.State.Pair()
				res.Failed[cluster+"|"+dep] = append(res.Failed[cluster+"|"+dep], e)
			}
		}
	}
	// a failed pair has no complete, redactable output: nothing of it stays
	for p := range res.Failed {
		cluster, dep, _ := strings.Cut(p, "|")
		if err := os.RemoveAll(filepath.Join(o.OutputDir, filepath.FromSlash(cluster), dep)); err != nil {
			return res, err
		}
	}
	return res, nil
}

// generate writes the states of the pairs into dir. One generation covers
// all pairs (filtered to their clusters and deployment names); when it
// fails, each pair is generated on its own, so a broken deployment only
// loses its own pair. The errors are per pair; err is for everything else.
func generate(eng *engine.Engine, dir string, pairs []string) (*renderer.Root, map[string]error, error) {
	var clusters, deps []string
	seenC, seenD := map[string]bool{}, map[string]bool{}
	for _, p := range pairs {
		c, d, _ := strings.Cut(p, "|")
		if !seenC[c] {
			seenC[c] = true
			clusters = append(clusters, c)
		}
		if !seenD[d] {
			seenD[d] = true
			deps = append(deps, d)
		}
	}
	opts := engine.GenerateOptions{Prepare: renderer.Prepare}
	if file, err := eng.Generate(filepath.Join(dir, "all"), engine.Filter{Clusters: clusters, Deployments: deps}, opts); err == nil {
		root, err := renderer.LoadRoot(file)
		return root, nil, err
	}
	errs := map[string]error{}
	var roots []*renderer.Root
	for i, p := range pairs {
		c, d, _ := strings.Cut(p, "|")
		file, err := eng.Generate(filepath.Join(dir, fmt.Sprintf("pair-%04d", i+1)), engine.Filter{Clusters: []string{c}, Deployments: []string{d}}, opts)
		if err != nil {
			errs[p] = err
			continue
		}
		root, err := renderer.LoadRoot(file)
		if err != nil {
			errs[p] = err
			continue
		}
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		return nil, errs, nil
	}
	root, err := renderer.Merge(dir, roots)
	return root, errs, err
}

func sortedKeys(m map[string]error) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
