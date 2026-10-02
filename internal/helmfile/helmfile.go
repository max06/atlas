// Package helmfile runs the helmfile binary against an ATLAS entry point.
//
// The ATLAS_* environment variables in Owned are the contract between this CLI
// and the ATLAS templates: helmfile templates can only read env vars and
// values, and a consumer's entry helmfile does not forward arbitrary values.
// The CLI is the only writer of that contract — those variables are dropped
// from the caller's environment, and only what Options says is set. Other
// ATLAS_* variables (debug knobs such as ATLAS_DEBUG_DISCOVERY) pass through.
package helmfile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Owned lists the ATLAS_* variables the CLI sets from flags.
var Owned = []string{
	"ATLAS_FILTER_CLUSTER", "ATLAS_FILTER_DEPLOYMENT_NAME", "ATLAS_SKIP_SECRETS",
	"ATLAS_REDACT_SECRETS", "ATLAS_DISCOVERY_MAP", "ATLAS_SIDEDUMP_MAP_DIR",
}

func owned(kv string) (string, bool) {
	name, _, _ := strings.Cut(kv, "=")
	for _, o := range Owned {
		if name == o {
			return name, true
		}
	}
	return name, false
}

// Options are the ATLAS runtime toggles. Each maps onto one ATLAS_* env var
// read by helmfile.yaml.gotmpl.
type Options struct {
	Clusters     []string // ATLAS_FILTER_CLUSTER (comma list)
	Deployments  []string // ATLAS_FILTER_DEPLOYMENT_NAME (comma list)
	SkipSecrets  bool     // ATLAS_SKIP_SECRETS=true
	Redact       bool     // ATLAS_REDACT_SECRETS=true
	DiscoveryMap bool     // ATLAS_DISCOVERY_MAP=1
	SidedumpDir  string   // ATLAS_SIDEDUMP_MAP_DIR
}

// Env returns the ATLAS_* variables for these options.
func (o Options) Env() []string {
	var env []string
	if len(o.Clusters) > 0 {
		env = append(env, "ATLAS_FILTER_CLUSTER="+strings.Join(o.Clusters, ","))
	}
	if len(o.Deployments) > 0 {
		env = append(env, "ATLAS_FILTER_DEPLOYMENT_NAME="+strings.Join(o.Deployments, ","))
	}
	if o.SkipSecrets {
		env = append(env, "ATLAS_SKIP_SECRETS=true")
	}
	if o.Redact {
		env = append(env, "ATLAS_REDACT_SECRETS=true")
	}
	if o.DiscoveryMap {
		env = append(env, "ATLAS_DISCOVERY_MAP=1")
	}
	if o.SidedumpDir != "" {
		env = append(env, "ATLAS_SIDEDUMP_MAP_DIR="+o.SidedumpDir)
	}
	return env
}

// Runner executes helmfile for one entry point.
type Runner struct {
	Binary string    // helmfile binary (default "helmfile")
	File   string    // entry helmfile, relative to Dir or absolute
	Dir    string    // working directory (the consumer repo root)
	Stderr io.Writer // helmfile's stderr (default os.Stderr)
	// ExtraEnv is appended after the ATLAS_* variables (e.g. SOPS_AGE_KEY_FILE).
	ExtraEnv []string
}

// baseEnv is the caller's environment without any ATLAS_* variable, plus the
// settings every ATLAS run needs.
func baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if _, ok := owned(kv); ok || strings.HasPrefix(kv, "HELMFILE_UPGRADE_NOTICE_DISABLED=") {
			continue
		}
		env = append(env, kv)
	}
	// `helmfile version` (ATLAS' version gate runs it) phones github.com for an
	// update notice; a TLS hiccup there fails the whole render.
	return append(env, "HELMFILE_UPGRADE_NOTICE_DISABLED=1")
}

// IgnoredAtlasEnv lists the Owned variables set in the caller's environment.
// The runner drops them; callers may warn about it.
func IgnoredAtlasEnv() []string {
	var names []string
	for _, kv := range os.Environ() {
		if name, ok := owned(kv); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Command builds the exec.Cmd for `helmfile -f <file> <args...>`.
func (r Runner) Command(ctx context.Context, opts Options, args ...string) *exec.Cmd {
	bin := r.Binary
	if bin == "" {
		bin = "helmfile"
	}
	full := append([]string{"-f", r.File}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = r.Dir
	cmd.Env = append(append(baseEnv(), opts.Env()...), r.ExtraEnv...)
	cmd.Stderr = r.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	return cmd
}

// Run runs helmfile, streaming stdout to w.
func (r Runner) Run(ctx context.Context, opts Options, w io.Writer, args ...string) error {
	cmd := r.Command(ctx, opts, args...)
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helmfile %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// Output runs helmfile and returns stdout.
func (r Runner) Output(ctx context.Context, opts Options, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := r.Run(ctx, opts, &out, args...)
	return out.Bytes(), err
}

// TemplateArgs are the `helmfile template` flags production renders use: the
// root ApplicationSet sets the same pair through HELMFILE_TEMPLATE_OPTIONS and
// HELM_TEMPLATE_OPTIONS. Without --include-crds, charts' crds/ dirs are left out.
func TemplateArgs() []string {
	return []string{
		"template",
		"--skip-schema-validation", "--include-crds",
		"--args", "--skip-schema-validation --include-crds",
	}
}

// OutputDirTemplate scatters a render into <dir>/<cluster>/<deployment>/<release>,
// the layout the review pipeline and the tests share.
const OutputDirTemplate = "{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}"
