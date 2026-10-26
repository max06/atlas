package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/config"
	"github.com/max06/atlas/internal/engine"
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
