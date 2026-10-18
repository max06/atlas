package classify

import (
	"reflect"
	"testing"

	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gitrev"
)

// The full rule matrix is exercised against classify.sh by the bats scenarios
// (ATLAS_CLI=... bats tests/bats/workflow/classify.bats); these cases pin the
// rules the shell version is hardest to read for.

func testMap(version int) *discovery.Map {
	return &discovery.Map{
		Version: version, DeploymentsRoot: "deployments", TemplatesRoot: "templates",
		Pairs: []discovery.Pair{
			{Cluster: "g1/c1", DeploymentName: "app-a", Templates: []string{"ns/a"}, Charts: []string{"charts/shared"}},
			{Cluster: "g1/c1", DeploymentName: "app-b", Templates: []string{"ns/b"}, Charts: []string{"charts/shared"}},
			{Cluster: "g1/c2", DeploymentName: "app-a", Templates: []string{"ns/a"}, Charts: []string{"charts/shared"}},
			{Cluster: "c3", DeploymentName: "db", Templates: []string{"db"}, Charts: []string{"templates/db/chart"}},
		},
	}
}

func classifyPath(base, pr *discovery.Map, path string) Result {
	return Classify(Input{Base: base, PR: pr, Entry: "helmfile.yaml.gotmpl",
		Changes: []gitrev.Change{{Status: "M", Path: path}}})
}

func TestRules(t *testing.T) {
	cases := []struct {
		path, mode, rule string
		pairs            []string
	}{
		{"deployments/g1/apps/app-a/values.yaml", "subset", "deployment", []string{"g1/c1|app-a", "g1/c2|app-a"}},
		{"deployments/g1/c1/cluster.values.yaml", "subset", "hierarchy", []string{"g1/c1|app-a", "g1/c1|app-b"}},
		{"deployments/global.values.yaml", "full", "", nil},
		{"deployments/g1/apps/README.md", "subset", "apps-dir-file", []string{}},
		{"templates/ns/a/helmfile.yaml.gotmpl", "subset", "template", []string{"g1/c1|app-a", "g1/c2|app-a"}},
		{"templates/ns/_glue.yaml.gotmpl", "subset", "template-namespace", []string{"g1/c1|app-a", "g1/c1|app-b", "g1/c2|app-a"}},
		{"templates/unused/x.yaml", "subset", "template", []string{}},
		{"templates/README.md", "full", "", nil},
		{"charts/shared/templates/x.yaml", "subset", "chart", []string{"g1/c1|app-a", "g1/c1|app-b", "g1/c2|app-a"}},
		{"charts/unused/Chart.yaml", "full", "", nil},
		{"helmfile.yaml.gotmpl", "full", "", nil},
		{"docs/x.md", "full", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			r := classifyPath(testMap(2), testMap(2), tc.path)
			if r.Mode != tc.mode {
				t.Fatalf("mode %s (%s), want %s", r.Mode, r.Reason, tc.mode)
			}
			if tc.mode == "full" {
				return
			}
			c := r.Changes[0]
			if c.Rule != tc.rule || !reflect.DeepEqual(c.Pairs, tc.pairs) {
				t.Fatalf("rule %s pairs %v, want %s %v", c.Rule, c.Pairs, tc.rule, tc.pairs)
			}
		})
	}
}

func TestChartRuleNeedsV2OnBothSides(t *testing.T) {
	r := classifyPath(testMap(1), testMap(2), "charts/shared/values.yaml")
	if r.Mode != "full" {
		t.Fatalf("v1 baseline map: mode %s, want full", r.Mode)
	}
}

func TestMissingMapForcesFull(t *testing.T) {
	r := classifyPath(nil, testMap(2), "deployments/g1/apps/app-a/values.yaml")
	if r.Mode != "full" || r.Reason != "no discovery map on the target branch" {
		t.Fatalf("got %s %q", r.Mode, r.Reason)
	}
}

func TestOneSidePairsAreAlwaysSelected(t *testing.T) {
	base, pr := testMap(2), testMap(2)
	pr.Pairs = append(pr.Pairs, discovery.Pair{Cluster: "g1/c9", DeploymentName: "app-a", Templates: []string{"ns/a"}})
	r := classifyPath(base, pr, "deployments/c3/apps/db/values.yaml")
	if r.Mode != "subset" || !reflect.DeepEqual(*r.SelectedPR, []string{"c3|db", "g1/c9|app-a"}) {
		t.Fatalf("selected_pr %v", *r.SelectedPR)
	}
	if !reflect.DeepEqual(*r.SelectedBaseline, []string{"c3|db"}) {
		t.Fatalf("selected_baseline %v", *r.SelectedBaseline)
	}
}
