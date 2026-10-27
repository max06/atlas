package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/gitrev"
	"github.com/max06/atlas/internal/promote"
)

func newPromoteCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promotion between branches of a tree by unit (experimental)",
	}
	cmd.AddCommand(newPromotePlanCmd(g))
	return cmd
}

func newPromotePlanCmd(g *globalFlags) *cobra.Command {
	var (
		from, to string
		skip     []string
		split    bool
		format   string
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "List the units that --from has and --to does not have",
		Long: `Lists what --from has that --to does not have, as units: changed
files grouped by the directory convention into atoms (a deployment directory,
a template, a local chart, the hierarchy files of one level, a top-level
directory), atoms linked when they reach the same (cluster, deployment) on
--to. Each unit gets a content check against --to with the merge base of the
two revisions: applies, conflicts (shown, not carried over) or present.

--split renders the pairs of each linked unit on --to (without the unit, with
all its atoms, with each atom alone; keyless) and separates atoms whose
renders do not interact.

--skip-cluster: clusters that do not deploy from --to (another branch); their
pairs are not counted as affected and do not link atoms.

The revisions are taken as they are in the clone (no fetch). The clone needs
the history down to the merge base.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from == "" || to == "" {
				return fmt.Errorf("--from and --to are required")
			}
			if format != "text" && format != "json" && format != "markdown" {
				return fmt.Errorf("--format %q: text, json or markdown", format)
			}
			repo, err := gitrev.Open(cmd.Context(), g.dir)
			if err != nil {
				return err
			}
			abs, err := filepath.Abs(filepath.Join(g.dir, g.file))
			if err != nil {
				return err
			}
			entry, err := filepath.Rel(repo.Dir, abs)
			if err != nil {
				return err
			}
			work, err := os.MkdirTemp("", "atlas-promote-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)
			plan, err := promote.Make(cmd.Context(), promote.Options{
				Repo: repo, From: from, To: to, Entry: entry, Helmfile: g.helmfile,
				SkipClusters: skip, Split: split, Work: filepath.Join(work, "w"), Log: os.Stderr,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch format {
			case "json":
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(plan)
			case "markdown":
				_, err = fmt.Fprint(out, promote.Markdown(plan))
			default:
				_, err = fmt.Fprint(out, promote.Text(plan))
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&from, "from", "", "revision with the changes (for example origin/canary)")
	f.StringVar(&to, "to", "", "revision to promote to (for example origin/main)")
	f.StringSliceVar(&skip, "skip-cluster", nil, "clusters not deployed from --to: full path or leaf name; repeat or comma-separate")
	f.BoolVar(&split, "split", false, "separate linked atoms by render on --to (keyless)")
	f.StringVar(&format, "format", "text", "text, json or markdown (the promote dashboard body)")
	return cmd
}
