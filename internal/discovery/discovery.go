// Package discovery reads the ATLAS discovery map: every (cluster, deployment)
// pair a tree renders, with the files and charts it depends on.
//
// The map is produced by ATLAS itself (ATLAS_DISCOVERY_MAP=1, see
// templates/helmfile.all.yaml.gotmpl): `helmfile build` then emits one
// release-less carrier state whose rendered values are the map.
package discovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/max06/atlas/internal/helmfile"
)

// MinVersion is the oldest map this CLI understands. Version 2 added
// per-pair local charts.
const MinVersion = 2

// Map is the discovery result of one tree.
type Map struct {
	Version int `json:"version" yaml:"version"`
	// Capabilities are the runtime switches the tree's ATLAS honors (map
	// header, see Capability*). Absent on older ATLAS versions.
	Capabilities    []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	DeploymentsRoot string   `json:"deploymentsRoot" yaml:"deploymentsRoot"`
	TemplatesRoot   string   `json:"templatesRoot" yaml:"templatesRoot"`
	// MacroParentTemplate: the structure template of deployments with
	// macros (atlas.macroParentTemplate of the entry; "" = none).
	MacroParentTemplate string `json:"macroParentTemplate,omitempty" yaml:"macroParentTemplate,omitempty"`
	Pairs               []Pair `json:"pairs" yaml:"pairs"`
}

// Pair is one (cluster, deployment) the tree renders.
type Pair struct {
	Cluster        string   `json:"cluster" yaml:"cluster"`
	ClusterName    string   `json:"clusterName" yaml:"clusterName"`
	ClusterGroup   string   `json:"clusterGroup,omitempty" yaml:"clusterGroup,omitempty"`
	DeploymentName string   `json:"deploymentName" yaml:"deploymentName"`
	DeploymentPath string   `json:"deploymentPath" yaml:"deploymentPath"`
	Templates      []string `json:"templates" yaml:"templates"`
	Charts         []string `json:"charts,omitempty" yaml:"charts,omitempty"`
	// Components: the components of a deployment with macros
	// (<deployment>-<entry>), rendered as children.
	Components []string `json:"components,omitempty" yaml:"components,omitempty"`
}

// Runtime switches an ATLAS version can list in the map header. An ATLAS
// that does not know a switch ignores its variable silently.
const (
	CapabilitySkipSecrets      = "skipSecrets"      // ATLAS_SKIP_SECRETS
	CapabilityMultiValueFilter = "multiValueFilter" // comma lists in ATLAS_FILTER_*
	CapabilityRedactSecrets    = "redactSecrets"    // ATLAS_REDACT_SECRETS
	CapabilityRedactMaps       = "redactMaps"       // ATLAS_REDACT_MAPS: maps through a carrier state, no post-renderer
	CapabilityChildRender      = "childRender"      // ATLAS_DEPLOYMENT + ATLAS_DEPLOYMENT_BASE (recursive ATLAS)
)

// Has reports whether the tree's ATLAS lists capability c. A nil map (ATLAS
// without map mode) has none.
func (m *Map) Has(c string) bool {
	if m == nil {
		return false
	}
	for _, have := range m.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}

// Key is the "<cluster>|<deployment>" form the review pipeline uses.
func (p Pair) Key() string { return p.Cluster + "|" + p.DeploymentName }

// ErrUnsupported means the tree's ATLAS predates discovery-map mode.
var ErrUnsupported = errors.New("the ATLAS version this tree pins has no discovery map (ATLAS_DISCOVERY_MAP)")

// probeCluster matches no cluster: a map-aware ATLAS still emits the carrier
// state (zero pairs), an older one emits nothing.
const probeCluster = "__atlas_discovery_probe__"

// Discover returns the map of the tree r points at.
//
// An ATLAS without map mode ignores ATLAS_DISCOVERY_MAP and runs a full state
// build — slow, and it needs SOPS keys. A cheap probe with a cluster filter
// that matches nothing tells the two apart first.
func Discover(ctx context.Context, r helmfile.Runner) (*Map, error) {
	if _, err := Probe(ctx, r); err != nil {
		return nil, err
	}
	m, err := build(ctx, r, helmfile.Options{DiscoveryMap: true})
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("discovery: probe found a map, the unfiltered build did not")
	}
	return m, nil
}

// Probe returns the map header (version, roots, capabilities) with zero
// pairs — well under a second, no SOPS. ErrUnsupported when the tree's ATLAS
// has no map mode.
func Probe(ctx context.Context, r helmfile.Runner) (*Map, error) {
	m, err := build(ctx, r, helmfile.Options{DiscoveryMap: true, Clusters: []string{probeCluster}})
	if err != nil {
		return nil, fmt.Errorf("discovery probe: %w", err)
	}
	if m == nil {
		return nil, ErrUnsupported
	}
	return m, nil
}

func build(ctx context.Context, r helmfile.Runner, opts helmfile.Options) (*Map, error) {
	out, err := r.Output(ctx, opts, "build", "--allow-no-matching-release")
	if err != nil {
		return nil, err
	}
	return Parse(out)
}

// Parse finds the map in a `helmfile build` YAML stream; nil when absent.
func Parse(stream []byte) (*Map, error) {
	dec := yaml.NewDecoder(bytes.NewReader(stream))
	for {
		var state struct {
			RenderedValues struct {
				AtlasDiscovery *Map `yaml:"atlasDiscovery"`
			} `yaml:"renderedvalues"`
		}
		err := dec.Decode(&state)
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse helmfile build output: %w", err)
		}
		if m := state.RenderedValues.AtlasDiscovery; m != nil {
			if m.Pairs == nil {
				m.Pairs = []Pair{}
			}
			return m, nil
		}
	}
}
