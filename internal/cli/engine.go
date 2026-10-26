package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/config"
	"github.com/max06/atlas/internal/engine"
	"github.com/max06/atlas/internal/helmfile"
)

// engineFlags select the engine of a command: the helmfile templates the
// entry helmfile pins (default), or the native Go engine of this binary.
type engineFlags struct {
	name        string
	templates   string
	deployments string
}

func (e *engineFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&e.name, "engine", "helmfile", "engine: helmfile (the templates the entry pins) or native (this binary, experimental)")
	f.StringVar(&e.templates, "templates", "", "native engine: app templates directory (default: atlas.yaml spec.templateDir, else templates)")
	f.StringVar(&e.deployments, "deployments", "", "native engine: deployments directory (default: atlas.yaml spec.deploymentDir, else deployments)")
}

func (e *engineFlags) native() bool { return e.name == "native" }

// layout is the tree of the native engine: the repository root (-C), the
// directories from the flags, else from atlas.yaml, else the defaults.
func (e *engineFlags) layout(g *globalFlags) (engine.Layout, error) {
	if e.name != "native" && e.name != "helmfile" {
		return engine.Layout{}, fmt.Errorf("--engine %q: helmfile or native", e.name)
	}
	root, err := filepath.Abs(g.dir)
	if err != nil {
		return engine.Layout{}, err
	}
	l := engine.Layout{Root: root, Templates: config.DefaultTemplateDir, Deployments: config.DefaultDeploymentDir}
	r, err := config.Load(root)
	switch {
	case err == nil:
		l.Templates, l.Deployments = r.Spec.TemplateDir, r.Spec.DeploymentDir
	case !errors.Is(err, config.ErrNotFound):
		return l, err
	}
	if e.templates != "" {
		l.Templates = e.templates
	}
	if e.deployments != "" {
		l.Deployments = e.deployments
	}
	return l, nil
}

// renderNative is `atlas render --engine native`: the engine writes the
// instance states and the values of every release, helmfile renders them.
func renderNative(cmd *cobra.Command, g *globalFlags, e *engineFlags, sel *selection, argocd bool, outputDir string, extra []string) error {
	if argocd {
		if err := sel.fromArgoCD(); err != nil {
			return err
		}
	}
	if sel.redact {
		return fmt.Errorf("--engine native: --redact is not implemented yet")
	}
	if sel.decrypt && sel.skipSecrets {
		return fmt.Errorf("--decrypt and --skip-secrets exclude each other")
	}
	l, err := e.layout(g)
	if err != nil {
		return err
	}
	clusters, err := resolveNativeClusters(l, sel.clusters)
	if err != nil {
		return err
	}
	eng, err := engine.New(l, engine.Switches{SkipSecrets: sel.keyless(argocd)})
	if err != nil {
		return err
	}
	args := helmfile.TemplateArgs()
	var opts engine.GenerateOptions
	r := helmfile.Runner{Binary: g.helmfile, File: "helmfile.yaml", Stderr: os.Stderr}
	if argocd {
		env, err := argoCDAppEnv()
		if err != nil {
			return err
		}
		r.ExtraEnv = append(r.ExtraEnv, env...)
		if os.Getenv("ARGOCD_ENV_HELMFILE_USE_CONTEXT_NAMESPACE") == "" {
			if ns := os.Getenv("ARGOCD_APP_NAMESPACE"); ns != "" {
				args = append([]string{"--namespace", ns}, args...)
				opts.NamespaceOverride = ns
			}
		}
	}
	dir, err := os.MkdirTemp("", "atlas-native-")
	if err != nil {
		return err
	}
	if os.Getenv("ATLAS_NATIVE_KEEP") == "" {
		defer os.RemoveAll(dir)
	} else {
		fmt.Fprintf(os.Stderr, "atlas: native render kept in %s\n", dir)
	}
	if _, err := eng.Generate(dir, engine.Filter{Clusters: clusters, Deployments: sel.deployments}, opts); err != nil {
		return err
	}
	r.Dir = dir
	if outputDir != "" {
		abs, err := filepath.Abs(outputDir)
		if err != nil {
			return err
		}
		args = append(args, "--output-dir", abs, "--output-dir-template", helmfile.OutputDirTemplate)
	}
	return r.Run(cmd.Context(), helmfile.Options{}, cmd.OutOrStdout(), append(args, extra...)...)
}

// resolveNativeClusters accepts full cluster paths and leaf names (like the
// helmfile backend), resolved against the native discovery.
func resolveNativeClusters(l engine.Layout, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	pairs, err := engine.Discover(l, engine.Filter{})
	if err != nil {
		return nil, err
	}
	known := map[string]string{} // leaf → full path
	full := map[string]bool{}
	for _, p := range pairs {
		known[p.ClusterName] = p.Cluster
		full[p.Cluster] = true
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		switch {
		case full[n]:
			out = append(out, n)
		case known[n] != "":
			out = append(out, known[n])
		default:
			return nil, fmt.Errorf("unknown cluster %q", n)
		}
	}
	return out, nil
}

// newNativeCmd is the hidden `atlas native` group: entry points of the native
// engine for tests and debugging.
func newNativeCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "native",
		Short:  "Native engine internals (tests, debugging)",
		Hidden: true,
		// generate reads the ATLAS_* variables on purpose: the contract of
		// the entry helmfile
		PersistentPreRun: func(*cobra.Command, []string) {},
	}
	var e engineFlags
	var out string
	gen := &cobra.Command{
		Use:   "generate",
		Short: "Write the native states of the tree into --out (root: <out>/helmfile.yaml)",
		Long: `Reads the runtime switches like the entry helmfile does: ATLAS_FILTER_CLUSTER,
ATLAS_FILTER_DEPLOYMENT_NAME (comma lists), ATLAS_SKIP_SECRETS=true,
ATLAS_REDACT_SECRETS=true, ATLAS_REDACT_MAPS=1, ATLAS_DISCOVERY_MAP=1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e.name = "native"
			l, err := e.layout(g)
			if err != nil {
				return err
			}
			eng, err := engine.New(l, engine.Switches{
				SkipSecrets:   os.Getenv("ATLAS_SKIP_SECRETS") == "true",
				RedactSecrets: os.Getenv("ATLAS_REDACT_SECRETS") == "true",
				RedactMaps:    os.Getenv("ATLAS_REDACT_MAPS") == "1",
			})
			if err != nil {
				return err
			}
			f := engine.Filter{Clusters: splitList(os.Getenv("ATLAS_FILTER_CLUSTER")), Deployments: splitList(os.Getenv("ATLAS_FILTER_DEPLOYMENT_NAME"))}
			if os.Getenv("ATLAS_DISCOVERY_MAP") == "1" {
				_, err = eng.GenerateDiscoveryMap(out, f)
				return err
			}
			_, err = eng.Generate(out, f, engine.GenerateOptions{})
			return err
		},
	}
	e.register(gen)
	gen.Flags().StringVar(&out, "out", "", "output directory")
	_ = gen.MarkFlagRequired("out")
	cmd.AddCommand(gen)
	return cmd
}
