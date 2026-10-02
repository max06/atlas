// Package cli wires the atlas commands.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/helmfile"
)

// Version is set at build time (-ldflags "-X github.com/max06/atlas/internal/cli.Version=v0.9.0").
var Version = "dev"

type globalFlags struct {
	dir      string // consumer repo root
	file     string // entry helmfile, relative to dir
	helmfile string // helmfile binary
}

// selection are the filter/secret flags shared by render-like commands.
type selection struct {
	clusters    []string
	deployments []string
	skipSecrets bool
	redact      bool
}

func (s *selection) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringSliceVarP(&s.clusters, "cluster", "c", nil, "render only these clusters (full path, e.g. group/cluster; repeat or comma-separate)")
	f.StringSliceVarP(&s.deployments, "deployment", "d", nil, "render only these deployments (repeat or comma-separate)")
	f.BoolVar(&s.skipSecrets, "skip-secrets", false, "never decrypt SOPS files: secret values render as their ENC[...] ciphertext (output not deployable, no key needed)")
	f.BoolVar(&s.redact, "redact", false, "redact secret values in the output (atlas-redact post-renderer; needs the helm plugin)")
}

func (s selection) options() helmfile.Options {
	return helmfile.Options{Clusters: s.clusters, Deployments: s.deployments, SkipSecrets: s.skipSecrets, Redact: s.redact}
}

// selectors mirrors the stage-1 filter as helmfile release selectors — the
// release-level safety net production renders use as well.
func (s selection) selectors() []string {
	var out []string
	switch {
	case len(s.clusters) > 0 && len(s.deployments) > 0:
		for _, c := range s.clusters {
			for _, d := range s.deployments {
				out = append(out, "--selector", "cluster="+c+",deploymentName="+d)
			}
		}
	case len(s.clusters) > 0:
		for _, c := range s.clusters {
			out = append(out, "--selector", "cluster="+c)
		}
	case len(s.deployments) > 0:
		for _, d := range s.deployments {
			out = append(out, "--selector", "deploymentName="+d)
		}
	}
	return out
}

// NewRoot builds the command tree.
func NewRoot() *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:   "atlas",
		Short: "Render, inspect and review ATLAS helmfile repositories",
		Long: `atlas drives an ATLAS consumer repository: it sets the ATLAS_* runtime
switches from flags, applies the production render defaults, and compares
revisions for reviews. It runs the helmfile binary; the ATLAS templates
themselves stay versioned by the repository's entry helmfile.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			if names := helmfile.IgnoredAtlasEnv(); len(names) > 0 {
				fmt.Fprintf(os.Stderr, "atlas: ignoring %s from the environment — use flags\n", strings.Join(names, ", "))
			}
		},
	}
	root.PersistentFlags().StringVarP(&g.dir, "dir", "C", ".", "consumer repository root")
	root.PersistentFlags().StringVarP(&g.file, "file", "f", "helmfile.yaml.gotmpl", "entry helmfile, relative to --dir")
	root.PersistentFlags().StringVar(&g.helmfile, "helmfile", "helmfile", "helmfile binary")

	root.AddCommand(newRenderCmd(g), newInspectCmd(g), newDiscoverCmd(g), newReviewCmd(g), newDoctorCmd(g), newVersionCmd())
	return root
}

func (g *globalFlags) runner() (helmfile.Runner, error) {
	dir, err := filepath.Abs(g.dir)
	if err != nil {
		return helmfile.Runner{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, g.file)); err != nil {
		return helmfile.Runner{}, fmt.Errorf("entry helmfile %s not found in %s", g.file, dir)
	}
	return helmfile.Runner{Binary: g.helmfile, File: g.file, Dir: dir, Stderr: os.Stderr}, nil
}

// Execute runs the CLI.
func Execute(ctx context.Context) int {
	if err := NewRoot().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "atlas:", err)
		return 1
	}
	return 0
}
