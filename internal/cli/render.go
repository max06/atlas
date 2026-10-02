package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/helmfile"
	"github.com/max06/atlas/internal/toolchain"
)

func newRenderCmd(g *globalFlags) *cobra.Command {
	var (
		sel       selection
		outputDir string
		argocd    bool
	)
	cmd := &cobra.Command{
		Use:   "render [-- extra helmfile template args]",
		Short: "Render manifests (helmfile template with the production defaults)",
		Long: `Render manifests to stdout, or with --output-dir into
<dir>/<cluster>/<deployment>/<release>/.

--argocd runs as the Argo CD config management plugin: the filter comes from
ARGOCD_ENV_ATLAS_FILTER_CLUSTER / ARGOCD_ENV_ATLAS_FILTER_DEPLOYMENT_NAME (the
Application's plugin env), and releases render into ARGOCD_APP_NAMESPACE unless
ARGOCD_ENV_HELMFILE_USE_CONTEXT_NAMESPACE is set.`,
		RunE: func(cmd *cobra.Command, extra []string) error {
			r, err := g.runner()
			if err != nil {
				return err
			}
			warnToolchain(cmd)
			args := helmfile.TemplateArgs()
			if argocd {
				if err := sel.fromArgoCD(); err != nil {
					return err
				}
				if os.Getenv("ARGOCD_ENV_HELMFILE_USE_CONTEXT_NAMESPACE") == "" {
					if ns := os.Getenv("ARGOCD_APP_NAMESPACE"); ns != "" {
						args = append([]string{"--namespace", ns}, args...)
					}
				}
			}
			args = append(args, sel.selectors()...)
			if outputDir != "" {
				args = append(args, "--output-dir", outputDir, "--output-dir-template", helmfile.OutputDirTemplate)
			}
			return r.Run(cmd.Context(), sel.options(), os.Stdout, append(args, extra...)...)
		},
	}
	sel.register(cmd)
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "write one directory per release instead of stdout")
	cmd.Flags().BoolVar(&argocd, "argocd", false, "Argo CD plugin mode (filter + namespace from the plugin environment)")
	return cmd
}

func (s *selection) fromArgoCD() error {
	if len(s.clusters) == 0 {
		s.clusters = splitList(os.Getenv("ARGOCD_ENV_ATLAS_FILTER_CLUSTER"))
	}
	if len(s.deployments) == 0 {
		s.deployments = splitList(os.Getenv("ARGOCD_ENV_ATLAS_FILTER_DEPLOYMENT_NAME"))
	}
	if len(s.clusters) == 0 || len(s.deployments) == 0 {
		return fmt.Errorf("--argocd: an Application renders exactly one cluster and deployment; set ARGOCD_ENV_ATLAS_FILTER_CLUSTER and ARGOCD_ENV_ATLAS_FILTER_DEPLOYMENT_NAME")
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func newInspectCmd(g *globalFlags) *cobra.Command {
	var (
		sel        selection
		withValues bool
	)
	cmd := &cobra.Command{
		Use:   "inspect [-- extra helmfile build args]",
		Short: "Print the helmfile states ATLAS generates (helmfile build) — no helm involved",
		RunE: func(cmd *cobra.Command, extra []string) error {
			r, err := g.runner()
			if err != nil {
				return err
			}
			args := append([]string{"build"}, sel.selectors()...)
			if !withValues {
				// embedded values may carry decrypted secrets; opt in explicitly
				args = append(args, "--embed-values=false")
			}
			return r.Run(cmd.Context(), sel.options(), os.Stdout, append(args, extra...)...)
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&withValues, "values", false, "embed rendered values (may contain decrypted secrets unless --skip-secrets)")
	return cmd
}

func newDiscoverCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "discover",
		Short: "Print the discovery map: every (cluster, deployment) with its templates and local charts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := g.runner()
			if err != nil {
				return err
			}
			// helmfile reports the (expected) empty probe on stderr; keep it
			// for errors only
			var log bytes.Buffer
			r.Stderr = &log
			m, err := discovery.Discover(cmd.Context(), r)
			if err != nil {
				return fmt.Errorf("%w\n%s", err, strings.TrimSpace(log.String()))
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(m)
		},
	}
}

func newDoctorCmd(_ *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check helm, helmfile and git against the versions this release is tested with",
		RunE: func(cmd *cobra.Command, _ []string) error {
			bad := false
			for _, t := range toolchain.Check(cmd.Context()) {
				found := t.Found
				if t.Err != nil {
					found = "-"
				}
				tested := t.Tested
				if tested == "" {
					tested = "-"
				}
				fmt.Printf("%-9s found %-8s tested %-8s min %-8s %s\n", t.Name, found, tested, t.Min, t.Status())
				if s := t.Status(); s == "missing" || s == "too old" {
					bad = true
				}
			}
			if bad {
				return fmt.Errorf("toolchain not usable")
			}
			return nil
		},
	}
}

// warnToolchain prints one line per tool that is not on its tested version.
func warnToolchain(cmd *cobra.Command) {
	for _, t := range toolchain.Check(cmd.Context()) {
		if t.Name == "git" {
			continue
		}
		switch t.Status() {
		case "untested":
			fmt.Fprintf(os.Stderr, "atlas: %s %s is not the tested %s (atlas doctor)\n", t.Name, t.Found, t.Tested)
		case "too old", "missing":
			fmt.Fprintf(os.Stderr, "atlas: %s %s (needs >= %s, atlas doctor)\n", t.Name, t.Status(), t.Min)
		}
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Run: func(*cobra.Command, []string) {
			fmt.Println(Version)
		},
	}
}
