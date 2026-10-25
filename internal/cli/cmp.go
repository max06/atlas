package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/config"
)

// DefaultPluginsDir is where the Argo CD repo-server and its plugin sidecars
// share the plugin sockets (<plugin>.sock).
const DefaultPluginsDir = "/home/argocd/cmp-server/plugins"

func newCmpCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cmp",
		Short: "Argo CD config management plugin helpers",
		Long: `Each engine minor runs as its own Argo CD plugin sidecar (atlas-v<major>-<minor>).
Applications do not name the plugin: every sidecar's discovery runs
"atlas cmp match", which reads atlas.yaml in the source path and matches only
the sidecar of the pinned engine. A consumer's own helmfile plugin for trees
without atlas.yaml runs "atlas cmp match --legacy".`,
	}
	cmd.AddCommand(newCmpMatchCmd(g), newCmpConfigCmd())
	return cmd
}

// matcher decides whether one plugin sidecar serves a tree.
type matcher struct {
	self       config.Engine // the sidecar's engine; unused when legacy
	legacy     bool          // the consumer's helmfile plugin: trees without atlas.yaml
	pluginsDir string
}

// match returns the line Argo CD takes as "match" (empty = no match). Errors
// are for files no sidecar may render (invalid atlas.yaml, a pin without an
// installed plugin); every sidecar reports the same error, because Argo CD
// shows only the error of the last sidecar it asked.
func (m matcher) match(dir string) (string, error) {
	root, err := config.Load(dir)
	if errors.Is(err, config.ErrNotFound) {
		if m.legacy {
			return "no " + config.FileName + ": legacy plugin", nil
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	pin := root.Spec.Engine
	if err := m.installed(pin); err != nil {
		return "", err
	}
	if !m.legacy && pin.Major == m.self.Major && pin.Minor == m.self.Minor {
		return fmt.Sprintf("%s pins engine %s: plugin %s", config.FileName, pin, pin.Plugin()), nil
	}
	return "", nil
}

// installed fails when no plugin socket serves the pin. Outside a repo-server
// (no plugins directory) there is nothing to check.
func (m matcher) installed(pin config.Engine) error {
	entries, err := os.ReadDir(m.pluginsDir)
	if err != nil {
		return nil
	}
	want := pin.Plugin()
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".sock")
		if !ok || e.Type()&fs.ModeSocket == 0 {
			continue
		}
		if name == want {
			return nil
		}
		names = append(names, name)
	}
	return fmt.Errorf("%s pins engine %q, but no Argo CD plugin %s is installed (installed: %s)", config.FileName, pin.String(), want, strings.Join(names, ", "))
}

// ownEngine is the engine minor of this build, or of --engine.
func ownEngine(flag string) (config.Engine, error) {
	if flag != "" {
		return config.ParseEngine(flag)
	}
	if e, ok := config.EngineOfVersion(Version); ok {
		return e, nil
	}
	return config.Engine{}, fmt.Errorf("this build (%s) has no release version: pass --engine <major>.<minor>", Version)
}

func newCmpMatchCmd(g *globalFlags) *cobra.Command {
	var engine string
	m := matcher{}
	cmd := &cobra.Command{
		Use:   "match",
		Short: "Plugin discovery: print a line when this sidecar renders the tree",
		Long: `Reads atlas.yaml in --dir (Argo CD runs discovery in the source path).
Exit 0 with output = match, exit 0 without output = another plugin, exit 1 =
no plugin may render the tree (the message reaches the Application status).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !m.legacy {
				e, err := ownEngine(engine)
				if err != nil {
					return err
				}
				m.self = e
			} else if engine != "" {
				return fmt.Errorf("--engine and --legacy exclude each other")
			}
			line, err := m.match(g.dir)
			if err != nil {
				return err
			}
			if line != "" {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&engine, "engine", "", "engine minor this sidecar serves (default: the minor of this release)")
	f.BoolVar(&m.legacy, "legacy", false, "match trees WITHOUT atlas.yaml (for the consumer's own helmfile plugin)")
	f.StringVar(&m.pluginsDir, "plugins-dir", DefaultPluginsDir, "directory of the Argo CD plugin sockets")
	return cmd
}

// pluginConfig is the ConfigManagementPlugin of one engine minor. A build
// without a release version gets a plugin without discovery ("atlas-dev"):
// it renders only Applications that name it.
func pluginConfig(e config.Engine, ok bool) string {
	if !ok {
		return `apiVersion: argoproj.io/v1alpha1
kind: ConfigManagementPlugin
metadata:
  name: atlas
spec:
  version: dev
  generate:
    command: [atlas, render, --argocd]
`
	}
	return fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
kind: ConfigManagementPlugin
metadata:
  name: atlas
spec:
  version: v%d-%d
  discover:
    find:
      command: [atlas, cmp, match, --engine, "%s"]
  generate:
    command: [atlas, render, --argocd]
`, e.Major, e.Minor, e)
}

func newCmpConfigCmd() *cobra.Command {
	var engine string
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the Argo CD plugin config (plugin.yaml) of this engine",
		Long: `The image bakes this output into /home/argocd/cmp-server/config/plugin.yaml.
A branch build has no release version: --engine makes it serve a minor (tests).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := ownEngine(engine)
			if err != nil && engine != "" {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), pluginConfig(e, err == nil))
			return nil
		},
	}
	cmd.Flags().StringVar(&engine, "engine", "", "engine minor to serve (default: the minor of this release)")
	return cmd
}
