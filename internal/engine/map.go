package engine

import (
	"fmt"
	"strings"

	"github.com/max06/atlas/internal/discovery"
)

// Capabilities of the native engine: the runtime switches it honors (the
// map header of the templates).
var Capabilities = []string{
	discovery.CapabilitySkipSecrets,
	discovery.CapabilityMultiValueFilter,
	discovery.CapabilityRedactSecrets,
	discovery.CapabilityRedactMaps,
}

// DiscoveryMap is the discovery map mode of helmfile.all.yaml.gotmpl:
// version 2, every pair with its repository-relative deployment.yaml path.
func DiscoveryMap(l Layout, f Filter) (*discovery.Map, error) {
	pairs, err := Discover(l, f)
	if err != nil {
		return nil, err
	}
	e := New(l, Switches{SkipSecrets: true})
	m := &discovery.Map{Version: 2, Capabilities: Capabilities, DeploymentsRoot: l.Deployments, TemplatesRoot: l.Templates, Pairs: []discovery.Pair{}}
	for _, p := range pairs {
		templates, charts, err := e.pairTemplatesAndCharts(p)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", p.Cluster, p.DeploymentName, err)
		}
		m.Pairs = append(m.Pairs, discovery.Pair{
			Cluster: p.Cluster, ClusterName: p.ClusterName, ClusterGroup: p.ClusterGroup,
			DeploymentName: p.DeploymentName,
			DeploymentPath: strings.TrimPrefix(p.DeploymentPath, l.Root+"/"),
			Templates:      templates,
			Charts:         charts,
		})
	}
	return m, nil
}
