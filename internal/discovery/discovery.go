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
	Version         int    `json:"version" yaml:"version"`
	DeploymentsRoot string `json:"deploymentsRoot" yaml:"deploymentsRoot"`
	TemplatesRoot   string `json:"templatesRoot" yaml:"templatesRoot"`
	Pairs           []Pair `json:"pairs" yaml:"pairs"`
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
	probe, err := build(ctx, r, helmfile.Options{DiscoveryMap: true, Clusters: []string{probeCluster}})
	if err != nil {
		return nil, fmt.Errorf("discovery probe: %w", err)
	}
	if probe == nil {
		return nil, ErrUnsupported
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
