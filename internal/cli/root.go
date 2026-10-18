// Package cli wires the atlas commands.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/discovery"
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
//
// Secrets: without --decrypt or --redact nothing is decrypted — SOPS values
// stay ENC[...], no key is needed, nothing secret reaches the terminal. That
// needs an ATLAS that honors ATLAS_SKIP_SECRETS (an older one ignores the
// variable and decrypts anyway), so prepare checks the map capabilities first.
type selection struct {
	clusters    []string
	deployments []string
	decrypt     bool
	skipSecrets bool // the default; kept so scripts that pass it keep working
	redact      bool
	// lenient: output never shows values (inspect without --values) — an
	// ATLAS without keyless support may decrypt, nothing reaches the output.
	lenient bool
}

// register adds the flags; verb names the command in the help texts
// ("render", "inspect"). withRedact adds --redact (post-renderer: render only).
func (s *selection) register(cmd *cobra.Command, verb string, withRedact bool) {
	f := cmd.Flags()
	f.StringSliceVarP(&s.clusters, "cluster", "c", nil, verb+" only these clusters: full path (group/cluster) or the leaf name; repeat or comma-separate")
	f.StringSliceVarP(&s.deployments, "deployment", "d", nil, verb+" only these deployments; repeat or comma-separate")
	f.BoolVar(&s.decrypt, "decrypt", false, "decrypt SOPS files (needs the key); default: secret values stay ENC[...], no key needed")
	f.BoolVar(&s.skipSecrets, "skip-secrets", false, "never decrypt SOPS files (the default)")
	_ = f.MarkHidden("skip-secrets")
	if withRedact {
		f.BoolVar(&s.redact, "redact", false, "decrypt, then redact secret values in the output (needs the atlas-redact helm plugin)")
	}
}

// keyless reports whether this run must not decrypt.
func (s selection) keyless(argocd bool) bool {
	return !(s.decrypt || s.redact || argocd)
}

func (s selection) options(argocd bool) helmfile.Options {
	return helmfile.Options{Clusters: s.clusters, Deployments: s.deployments, SkipSecrets: s.keyless(argocd), Redact: s.redact}
}

// prepare validates the flags against the tree's ATLAS before a render:
// keyless rendering and multi-value filters need the matching capability,
// leaf cluster names are resolved to full paths. Argo CD mode skips all of it
// (one cluster, one deployment, full path, decrypting — the production path).
func (s *selection) prepare(ctx context.Context, r helmfile.Runner, argocd bool) error {
	if s.decrypt && s.skipSecrets {
		return fmt.Errorf("--decrypt and --skip-secrets exclude each other")
	}
	if argocd {
		return nil
	}
	multi := len(s.clusters) > 1 || len(s.deployments) > 1
	short := false
	for _, c := range s.clusters {
		short = short || !strings.Contains(c, "/")
	}
	if !s.keyless(false) && !multi && !short {
		return nil
	}
	qr, log := quiet(r)
	probe, err := discovery.Probe(ctx, qr)
	if err != nil && !errors.Is(err, discovery.ErrUnsupported) {
		return withLog(err, log)
	}
	if s.keyless(false) && !probe.Has(discovery.CapabilitySkipSecrets) {
		if s.lenient {
			s.decrypt = true
			return s.prepare(ctx, r, argocd)
		}
		return fmt.Errorf("the ATLAS version this repository pins (%s) cannot render without decrypting SOPS files — pass --decrypt (needs the key) or update the pin", r.File)
	}
	if multi && !probe.Has(discovery.CapabilityMultiValueFilter) {
		return fmt.Errorf("the ATLAS version this repository pins (%s) renders one cluster and one deployment per filter — pass a single -c/-d or update the pin", r.File)
	}
	if !short {
		return nil
	}
	qr, log = quiet(r)
	m, err := discovery.Discover(ctx, qr)
	if err != nil {
		return withLog(fmt.Errorf("resolve cluster names: %w", err), log)
	}
	for i, c := range s.clusters {
		if s.clusters[i], err = m.ResolveCluster(c); err != nil {
			return err
		}
	}
	return nil
}

// explain turns a failed filtered render into a name error when a cluster or
// deployment does not exist (helmfile only says "no releases found").
func (s selection) explain(ctx context.Context, r helmfile.Runner, renderErr error) error {
	if len(s.clusters) == 0 && len(s.deployments) == 0 {
		return renderErr
	}
	qr, _ := quiet(r)
	m, err := discovery.Discover(ctx, qr)
	if err != nil {
		return renderErr
	}
	for _, c := range s.clusters {
		if _, err := m.ResolveCluster(c); err != nil {
			return err
		}
	}
	for _, d := range s.deployments {
		if err := m.CheckDeployment(d); err != nil {
			return err
		}
	}
	return renderErr
}

// quiet captures helmfile's stderr instead of printing it: the probe's
// expected "no releases found" notice is noise, but on a failure the log is
// the only place that says why (withLog).
func quiet(r helmfile.Runner) (helmfile.Runner, *bytes.Buffer) {
	var buf bytes.Buffer
	r.Stderr = &buf
	return r, &buf
}

// withLog appends the last lines of a captured helmfile log to err.
func withLog(err error, log *bytes.Buffer) error {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(log.String()), "\n") {
		if l != "" && !strings.Contains(l, "no releases found that matches specified selector") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return err
	}
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return fmt.Errorf("%w\n  %s", err, strings.Join(lines, "\n  "))
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
		if code, ok := exitCode(err); ok {
			return code
		}
		fmt.Fprintln(os.Stderr, "atlas:", err)
		return 1
	}
	return 0
}
