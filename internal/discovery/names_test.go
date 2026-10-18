package discovery

import (
	"strings"
	"testing"
)

func testMap() *Map {
	return &Map{Version: 2, Capabilities: []string{CapabilitySkipSecrets}, Pairs: []Pair{
		{Cluster: "application/virtual-canary", ClusterName: "virtual-canary", DeploymentName: "httpbin"},
		{Cluster: "infra/zulu", ClusterName: "zulu", DeploymentName: "httpbin"},
		{Cluster: "infra/zulu", ClusterName: "zulu", DeploymentName: "rook-ceph-cluster"},
		{Cluster: "mgmt", ClusterName: "mgmt", DeploymentName: "argocd"},
	}}
}

func TestResolveCluster(t *testing.T) {
	m := testMap()
	for in, want := range map[string]string{
		"infra/zulu":     "infra/zulu",
		"zulu":           "infra/zulu",
		"virtual-canary": "application/virtual-canary",
		"mgmt":           "mgmt",
	} {
		got, err := m.ResolveCluster(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestResolveClusterUnknownSuggests(t *testing.T) {
	_, err := testMap().ResolveCluster("canary")
	if err == nil || !strings.Contains(err.Error(), "did you mean: application/virtual-canary") {
		t.Fatalf("got %v", err)
	}
	_, err = testMap().ResolveCluster("alpha")
	if err == nil || !strings.Contains(err.Error(), "known: application/virtual-canary, infra/zulu, mgmt") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckDeployment(t *testing.T) {
	m := testMap()
	if err := m.CheckDeployment("httpbin"); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckDeployment("rook"); err == nil || !strings.Contains(err.Error(), "did you mean: rook-ceph-cluster") {
		t.Fatalf("got %v", err)
	}
}

func TestHas(t *testing.T) {
	var none *Map
	if none.Has(CapabilitySkipSecrets) || !testMap().Has(CapabilitySkipSecrets) || testMap().Has(CapabilityRedactSecrets) {
		t.Fatal("Has")
	}
}
