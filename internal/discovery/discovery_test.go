package discovery

import "testing"

func TestParseFindsTheCarrierState(t *testing.T) {
	stream := []byte(`---
filepath: helmfile.yaml.gotmpl
---
filepath: templates/helmfile.discovery-map.yaml
renderedvalues:
  atlasDiscovery:
    version: 2
    deploymentsRoot: deployments
    templatesRoot: templates
    pairs:
      - cluster: g/c
        clusterName: c
        clusterGroup: g
        deploymentName: web
        deploymentPath: deployments/g/c/apps/web/deployment.yaml
        templates: [web]
        charts: [charts/web]
`)
	m, err := Parse(stream)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil || m.Version != 2 || len(m.Pairs) != 1 || m.Pairs[0].Key() != "g/c|web" || m.Pairs[0].Charts[0] != "charts/web" {
		t.Fatalf("got %+v", m)
	}
}

func TestParseWithoutMapIsNil(t *testing.T) {
	m, err := Parse([]byte("---\nreleases:\n  - name: r\n"))
	if err != nil || m != nil {
		t.Fatalf("got %+v, %v", m, err)
	}
}

func TestParseEmptyPairList(t *testing.T) {
	m, err := Parse([]byte("renderedvalues:\n  atlasDiscovery:\n    version: 2\n    pairs: []\n"))
	if err != nil || m == nil || m.Pairs == nil || len(m.Pairs) != 0 {
		t.Fatalf("got %+v, %v", m, err)
	}
}
