package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/ci"
	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gitrev"
	"github.com/max06/atlas/internal/report"
	"github.com/max06/atlas/internal/review"
	"github.com/max06/atlas/internal/snapshot"
)

type revFlags struct {
	spec review.Spec
}

func (f *revFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.spec.Base, "base", "", "target revision (default: <remote>/<default branch> as it is in the clone, never fetched; a branch cut from canary wants --base origin/canary)")
	fl.StringVar(&f.spec.Head, "head", "", "revision to review (default: the working tree, incl. uncommitted and untracked files)")
	fl.StringVar(&f.spec.Remote, "remote", "origin", "remote for the default base")
	fl.BoolVar(&f.spec.NoMerge, "no-merge", false, "compare --base and --head directly instead of base vs merge result (CI: the platform already merged)")
	fl.BoolVar(&f.spec.SingleEngine, "single-engine", false, "render both sides with this atlas, also when a side's atlas.yaml pins another engine (shows content changes, hides what an engine change does)")
}

func newReviewCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Compare two revisions: what a change can affect, and how renders differ",
		Long: `Reviews answer "what changes if this merges now": the base is the target
branch as it is in your clone (by default the remote-tracking ref, like
"git diff origin/main"; the CLI never fetches, so run "git fetch" yourself
when the printed base date looks old), the other side is the merge result,
built locally with git merge-tree.`,
	}
	cmd.AddCommand(newReviewClassifyCmd(g), newReviewDiffCmd(g), newReviewCICmd(g))
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
	f.spec.Engine = Version
	s, err := review.Open(cmd.Context(), repo, entry, f.spec, os.Stderr)
	if err != nil {
		return nil, err
	}
	s.Binary = g.helmfile
	return s, nil
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
				in, err := ci.ClassifyInput(mapBase, mapHead, changesFile, g.file)
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
	fl.StringVar(&changesFile, "changes-file", "", "precomputed: output of git diff --name-status --no-renames")
	return cmd
}

func keylessWord(m *discovery.Map) string {
	if m == nil {
		return "no discovery map (older ATLAS)"
	}
	if m.Has(discovery.CapabilitySkipSecrets) {
		return "keyless ok"
	}
	return "decrypts"
}

func parenIf(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func newReviewDiffCmd(g *globalFlags) *cobra.Command {
	var (
		f              revFlags
		sel            selection
		outDir         string
		full           bool
		patch          bool
		asJSON         bool
		parallel       int
		allowBaseError bool
		format         string
	)
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Render the affected pairs on both sides and list the releases that differ",
		Long: `Classifies the change, renders the selection on both sides (everything with
--full or when the classifier asks for it) from ONE workspace path, and lists
releases that differ. --patch prints the file diffs as well.

-c/-d replace the classifier: exactly these clusters and deployments render on
both sides (comma lists or repeated flags; leaf cluster names resolve like
render), also when the change does not reach them.

--format text (default) lists the releases whose rendered files differ;
--format markdown prints the PR comment the review workflow would post
(semantic per-resource diffs, render scope) — the same code as the CI review.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "text" && format != "markdown" {
				return fmt.Errorf("--format %q: text or markdown", format)
			}
			s, err := openSession(cmd, g, f)
			if err != nil {
				return err
			}
			defer s.Close()
			c, err := s.Classify(cmd.Context())
			if err != nil {
				return err
			}
			if len(sel.clusters) > 0 || len(sel.deployments) > 0 {
				if full {
					return fmt.Errorf("--full and -c/-d exclude each other")
				}
				if err := c.Select(sel.clusters, sel.deployments); err != nil {
					return err
				}
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
			if sel.decrypt && sel.skipSecrets {
				return fmt.Errorf("--decrypt and --skip-secrets exclude each other")
			}
			keyless := sel.keyless(false)
			var baseErr error
			if c.BaseBroken() {
				baseErr = c.BaseErr
			}
			if keyless {
				// each side renders with the ATLAS its own entry pins; a side
				// without keyless support would decrypt, and the diff would put
				// its plaintext next to the other side's ENC[...] values
				baseOK := c.BaseMap.Has(discovery.CapabilitySkipSecrets) || baseErr != nil || errors.Is(c.BaseErr, review.ErrNoEntry)
				if !baseOK || !c.HeadMap.Has(discovery.CapabilitySkipSecrets) {
					return fmt.Errorf("the ATLAS version a side pins cannot render without decrypting SOPS files (target: %s, reviewed: %s) — pass --decrypt (needs the key) or update the pin",
						keylessWord(c.BaseMap), keylessWord(c.HeadMap))
				}
			}
			opts := review.RenderOptions{SkipSecrets: keyless, Redact: sel.redact, Parallel: parallel, BaseErr: baseErr}
			res, err := s.Render(cmd.Context(), c, outDir, opts)
			if err != nil {
				return err
			}
			if res.Base == review.StatusError {
				if !allowBaseError {
					return fmt.Errorf("the target side does not render, so every release of the selection would show as added; fix the target branch or pass --allow-base-error:\n%v", res.BaseErr)
				}
				fmt.Fprintf(os.Stderr, "atlas: target side does not render (--allow-base-error): releases that exist on both sides show as added\n%v\n", res.BaseErr)
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
			if format == "markdown" {
				return printComment(cmd, c, outDir, pairs, res, g.file, full)
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
	sel.register(cmd, "render", true)
	fl := cmd.Flags()
	fl.StringVarP(&outDir, "out", "o", "", "keep both render trees here (<out>/base, <out>/head)")
	fl.BoolVar(&full, "full", false, "render everything instead of the classified subset")
	fl.BoolVar(&patch, "patch", false, "print file diffs of the changed releases")
	fl.BoolVar(&asJSON, "json", false, "print the changed releases as JSON")
	fl.IntVar(&parallel, "parallel", 4, "concurrent helmfile invocations")
	fl.BoolVar(&allowBaseError, "allow-base-error", false, "compare even when the target side does not render (the change may be the fix)")
	fl.StringVar(&format, "format", "text", "text: changed releases; markdown: the PR comment of the review workflow")
	return cmd
}

// printComment prints the review comment for two local render trees: the
// semantic diff of internal/snapshot and the markdown of internal/report, as
// the CI review builds it. Nothing to replay: a local review renders keyless
// (or decrypts on request) on both sides with the same ATLAS.
func printComment(cmd *cobra.Command, c *review.Classification, outDir string, pairs []string, res review.RenderResult, entry string, full bool) error {
	d, err := snapshot.Diff(cmd.Context(), snapshot.DiffOptions{
		BaselineDir: filepath.Join(outDir, "base"), PRDir: filepath.Join(outDir, "head"), Pairs: pairs,
	})
	if err != nil {
		return err
	}
	blocks := report.Blocks(d)
	subsetMode, renderMode := "on", c.Mode
	if full {
		subsetMode, renderMode = "off", "full"
	}
	if renderMode != "subset" {
		renderMode = "full"
	}
	scope, _ := report.Scope(report.ScopeInput{
		SubsetMode: subsetMode, RenderMode: renderMode,
		PRStatus: res.Head, BaselineStatus: res.Base, Classify: &c.Result,
	})
	in := report.CommentInput{
		BaselineStatus: res.Base, PRStatus: res.Head, DiffStatus: d.Status,
		BaselineFilter: true, PRFilter: true,
		DiffTotal: d.Resources(), DiffReleases: len(d.Releases),
		// locally nothing limits the size: the complete blocks
		DiffBlocks: blocks.Summary, Scope: scope, HelmfilePath: entry,
	}
	if res.BaseErr != nil {
		in.BaselineErrors = res.BaseErr.Error()
	}
	fmt.Print(report.Comment(in))
	return nil
}
