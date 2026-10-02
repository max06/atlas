package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gitrev"
	"github.com/max06/atlas/internal/review"
)

type revFlags struct {
	spec review.Spec
}

func (f *revFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.spec.Base, "base", "", "target revision (default <remote>/<default branch>, fetched first)")
	fl.StringVar(&f.spec.Head, "head", "", "revision to review (default: the working tree, incl. uncommitted and untracked files)")
	fl.StringVar(&f.spec.Remote, "remote", "origin", "remote for the default base")
	fl.BoolVar(&f.spec.NoMerge, "no-merge", false, "compare --base and --head directly instead of base vs merge result (CI: the platform already merged)")
	fl.BoolVar(&f.spec.Offline, "offline", false, "do not fetch the base ref")
}

func newReviewCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Compare two revisions: what a change can affect, and how renders differ",
		Long: `Reviews answer "what changes if this merges now": the base is the CURRENT
tip of the target branch (by default the remote-tracking ref, fetched first —
a stale local branch would show newer target commits as reverts), the other
side is the merge result, built locally with git merge-tree.`,
	}
	cmd.AddCommand(newReviewClassifyCmd(g), newReviewDiffCmd(g))
	return cmd
}

func openSession(cmd *cobra.Command, g *globalFlags, f revFlags) (*review.Session, error) {
	repo, err := gitrev.Open(cmd.Context(), g.dir)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(filepath.Join(g.dir, g.file))
	if err != nil {
		return nil, err
	}
	entry, err := filepath.Rel(repo.Dir, abs)
	if err != nil {
		return nil, err
	}
	if repo.Shallow(cmd.Context()) && !f.spec.NoMerge {
		fmt.Fprintln(os.Stderr, "atlas: shallow clone — a local merge needs history; CI should pass both commits with --no-merge")
	}
	return review.Open(cmd.Context(), repo, entry, f.spec, os.Stderr)
}

func newReviewClassifyCmd(g *globalFlags) *cobra.Command {
	var (
		f            revFlags
		outDir       string
		githubOutput string
		mapBase      string
		mapHead      string
		changesFile  string
	)
	cmd := &cobra.Command{
		Use:   "classify",
		Short: "Select the (cluster, deployment) pairs a change can affect",
		Long: `Prints the classification as JSON (the classify.json of the review
pipeline). With --out, writes the full classify.sh output set into a directory.

--map-base/--map-head/--changes-file classify precomputed inputs without git
or helmfile (drop-in for .github/actions/atlas-render/classify.sh).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var result classify.Result
			if mapBase != "" || mapHead != "" || changesFile != "" {
				in, err := precomputedInput(mapBase, mapHead, changesFile, g.file)
				if err != nil {
					return err
				}
				result = classify.Classify(in)
			} else {
				s, err := openSession(cmd, g, f)
				if err != nil {
					return err
				}
				defer s.Close()
				c, err := s.Classify(cmd.Context())
				if err != nil {
					return err
				}
				result = c.Result
			}
			if outDir != "" {
				if err := classify.WriteFiles(outDir, result, githubOutput); err != nil {
					return err
				}
			}
			fmt.Fprintf(os.Stderr, "classify: mode=%s%s\n", result.Mode, parenIf(result.Reason))
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		},
	}
	f.register(cmd)
	fl := cmd.Flags()
	fl.StringVar(&outDir, "out", "", "write classify.json, pairs-*.txt and classify-summary.md here")
	fl.StringVar(&githubOutput, "github-output", os.Getenv("GITHUB_OUTPUT"), "append step outputs here (with --out)")
	fl.StringVar(&mapBase, "map-base", "", "precomputed: discovery map JSON of the target side (empty file = none)")
	fl.StringVar(&mapHead, "map-head", "", "precomputed: discovery map JSON of the merge result (empty file = none)")
	fl.StringVar(&changesFile, "changes-file", "", "precomputed: `git diff --name-status --no-renames` output")
	return cmd
}

func parenIf(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func precomputedInput(mapBase, mapHead, changesFile, entry string) (classify.Input, error) {
	in := classify.Input{Entry: entry}
	var err error
	if in.Base, err = readMap(mapBase); err != nil {
		return in, err
	}
	if in.PR, err = readMap(mapHead); err != nil {
		return in, err
	}
	data, err := os.ReadFile(changesFile)
	if err != nil {
		return in, err
	}
	in.Changes = gitrev.ParseNameStatus(string(data))
	return in, nil
}

func readMap(path string) (*discovery.Map, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, nil // missing/empty = this side has no map → full render
	}
	var m discovery.Map
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

func newReviewDiffCmd(g *globalFlags) *cobra.Command {
	var (
		f        revFlags
		sel      selection
		outDir   string
		full     bool
		patch    bool
		asJSON   bool
		parallel int
	)
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Render the affected pairs on both sides and list the releases that differ",
		Long: `Classifies the change, renders the selection on both sides (everything with
--full or when the classifier asks for it) from ONE workspace path, and lists
releases that differ. --patch prints the file diffs as well.

This proof of concept compares rendered files byte for byte; the semantic
diff and PR comment of the review pipeline (atlas-diff) are not ported yet.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := openSession(cmd, g, f)
			if err != nil {
				return err
			}
			defer s.Close()
			c, err := s.Classify(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "classify: mode=%s%s\n", c.Mode, parenIf(c.Reason))
			if full {
				c.Mode = "full"
			}
			if outDir == "" {
				tmp, err := os.MkdirTemp("", "atlas-review-out-")
				if err != nil {
					return err
				}
				defer os.RemoveAll(tmp)
				outDir = tmp
			}
			opts := review.RenderOptions{SkipSecrets: sel.skipSecrets, Redact: sel.redact, Parallel: parallel}
			if err := s.Render(cmd.Context(), c, outDir, opts); err != nil {
				return err
			}
			var pairs []string
			for _, m := range []*discovery.Map{c.BaseMap, c.HeadMap} {
				if m != nil {
					for _, p := range m.Pairs {
						pairs = append(pairs, p.Key())
					}
				}
			}
			changes, err := review.Compare(filepath.Join(outDir, "base"), filepath.Join(outDir, "head"), pairs)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(changes)
			}
			if len(changes) == 0 {
				fmt.Println("no changes in rendered manifests")
				return nil
			}
			for _, ch := range changes {
				fmt.Printf("%-8s %s\n", ch.Kind, ch.Release)
				if patch {
					d := exec.Command("git", "diff", "--no-index", "--no-color",
						filepath.Join("base", ch.Release), filepath.Join("head", ch.Release))
					d.Dir = outDir // short a/base/... b/head/... paths
					d.Stdout, d.Stderr = os.Stdout, os.Stderr
					_ = d.Run() // exit 1 = differences
				}
			}
			return nil
		},
	}
	f.register(cmd)
	sel.register(cmd)
	_ = cmd.Flags().MarkHidden("cluster")
	_ = cmd.Flags().MarkHidden("deployment")
	fl := cmd.Flags()
	fl.StringVarP(&outDir, "out", "o", "", "keep both render trees here (<out>/base, <out>/head)")
	fl.BoolVar(&full, "full", false, "render everything instead of the classified subset")
	fl.BoolVar(&patch, "patch", false, "print file diffs of the changed releases")
	fl.BoolVar(&asJSON, "json", false, "print the changed releases as JSON")
	fl.IntVar(&parallel, "parallel", 4, "concurrent helmfile invocations")
	return cmd
}
