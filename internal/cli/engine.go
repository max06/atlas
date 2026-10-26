package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"gopkg.in/yaml.v3"

	"github.com/max06/atlas/internal/config"
	"github.com/max06/atlas/internal/discovery"
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
// Like the helmfile backend: leaf cluster names resolve and unknown names
// fail with suggestions (not in Argo CD mode, where the plugin env is
// taken as given), and the stage-1 filter is backed by release selectors.
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
	if !argocd {
		if err := resolveNativeNames(l, sel); err != nil {
			return err
		}
	}
	eng, err := engine.New(l, engine.Switches{SkipSecrets: sel.keyless(argocd)})
	if err != nil {
		return err
	}
	args := helmfile.TemplateArgs()
	r := helmfile.Runner{Binary: g.helmfile, File: "helmfile.yaml", Stderr: os.Stderr}
	opts := engine.GenerateOptions{}
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
		fmt.Fprintf(os.Stderr, "atlas: native render kept in %s (values files may hold decrypted secrets)\n", dir)
	}
	r.Dir = dir
	if truthy(os.Getenv("HELMFILE_CACHE_CLEANUP")) || truthy(os.Getenv("ARGOCD_ENV_HELMFILE_CACHE_CLEANUP")) {
		if err := r.Run(cmd.Context(), helmfile.Options{}, io.Discard, "cache", "cleanup"); err != nil {
			return err
		}
	}
	opts.Prepare = buildPreparer(cmd.Context(), r)
	if _, err := eng.Generate(dir, engine.Filter{Clusters: sel.clusters, Deployments: sel.deployments}, opts); err != nil {
		return err
	}
	args = append(args, sel.selectors()...)
	if outputDir != "" {
		abs, err := filepath.Abs(outputDir)
		if err != nil {
			return err
		}
		args = append(args, "--output-dir", abs, "--output-dir-template", helmfile.OutputDirTemplate)
	}
	return r.Run(cmd.Context(), helmfile.Options{}, cmd.OutOrStdout(), append(args, extra...)...)
}

// buildPreparer runs `helmfile build` on the generated root: the releases
// as helmfile prepares them (rendered release templates, commonLabels,
// defaults) are the .Release of the values computation.
func buildPreparer(ctx context.Context, r helmfile.Runner) engine.Preparer {
	return func(root string) (map[string][]engine.PreparedRelease, error) {
		var log bytes.Buffer
		br := r
		br.File, br.Dir, br.Stderr = root, filepath.Dir(root), &log
		out, err := br.Output(ctx, helmfile.Options{}, "build")
		if err != nil {
			return nil, withLog(fmt.Errorf("native engine: helmfile build of the generated states: %w", err), &log)
		}
		prepared := map[string][]engine.PreparedRelease{}
		dec := yaml.NewDecoder(bytes.NewReader(out))
		for {
			var state struct {
				FilePath string `yaml:"filepath"`
				Releases []struct {
					Name        string            `yaml:"name"`
					Namespace   string            `yaml:"namespace"`
					Chart       string            `yaml:"chart"`
					Version     string            `yaml:"version"`
					KubeContext string            `yaml:"kubeContext"`
					Labels      map[string]string `yaml:"labels"`
				} `yaml:"releases"`
			}
			err := dec.Decode(&state)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("native engine: parse helmfile build output: %w", err)
			}
			var releases []engine.PreparedRelease
			for _, rel := range state.Releases {
				releases = append(releases, engine.PreparedRelease{Name: rel.Name, Namespace: rel.Namespace, Chart: rel.Chart, Version: rel.Version, KubeContext: rel.KubeContext, Labels: rel.Labels})
			}
			if _, seen := prepared[state.FilePath]; !seen {
				prepared[state.FilePath] = releases
			}
		}
		return prepared, nil
	}
}

// resolveNativeNames resolves leaf cluster names to full paths and checks
// the deployment names, with the messages of the helmfile backend.
func resolveNativeNames(l engine.Layout, sel *selection) error {
	if len(sel.clusters) == 0 && len(sel.deployments) == 0 {
		return nil
	}
	pairs, err := engine.Discover(l, engine.Filter{})
	if err != nil {
		return err
	}
	m := &discovery.Map{}
	for _, p := range pairs {
		m.Pairs = append(m.Pairs, discovery.Pair{Cluster: p.Cluster, ClusterName: p.ClusterName, DeploymentName: p.DeploymentName})
	}
	for i, c := range sel.clusters {
		if sel.clusters[i], err = m.ResolveCluster(c); err != nil {
			return err
		}
	}
	for _, d := range sel.deployments {
		if err := m.CheckDeployment(d); err != nil {
			return err
		}
	}
	return nil
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
	var out, namespace string
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
			_, err = eng.Generate(out, f, engine.GenerateOptions{
				NamespaceOverride: namespace,
				Prepare:           buildPreparer(cmd.Context(), helmfile.Runner{Binary: g.helmfile, Stderr: os.Stderr}),
			})
			return err
		},
	}
	e.register(gen)
	gen.Flags().StringVar(&namespace, "namespace", "", "helmfile's --namespace of the render (the .Release.Namespace of the values)")
	gen.Flags().StringVar(&out, "out", "", "output directory")
	_ = gen.MarkFlagRequired("out")
	cmd.AddCommand(gen)
	return cmd
}
