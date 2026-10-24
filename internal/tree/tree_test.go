package tree

import (
	"fmt"
	"strings"
	"testing"
)

// app returns one Application document. definition "" = an on-disk
// Application (filter only), else a child call.
func app(ns, name, cluster, deployment, definition, parents string) string {
	env := fmt.Sprintf(`
        - {name: ATLAS_FILTER_CLUSTER, value: %q}
        - {name: ATLAS_FILTER_DEPLOYMENT_NAME, value: %q}`, cluster, deployment)
	if definition != "" {
		env += fmt.Sprintf(`
        - {name: ATLAS_DEPLOYMENT, value: %q}
        - {name: ATLAS_DEPLOYMENT_BASE, value: "deployments/%s/.child"}
        - {name: ATLAS_PARENTS, value: %q}`, definition, deployment, parents)
	}
	return fmt.Sprintf(`---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: %s, namespace: %s}
spec:
  source:
    plugin:
      env:%s
`, name, ns, env)
}

const configMap = `---
apiVersion: v1
kind: ConfigMap
metadata: {name: leaf}
`

// fake renders a call from a table keyed by deployment name.
func fake(table map[string]string, seen *[]string) RenderFunc {
	return func(c Call) ([]byte, error) {
		*seen = append(*seen, c.Cluster+"/"+c.Deployment)
		out, ok := table[c.Deployment]
		if !ok {
			return nil, fmt.Errorf("no render for %s", c.Deployment)
		}
		return []byte(out), nil
	}
}

func TestAppsParsesCallsAndOnDisk(t *testing.T) {
	in := configMap + app("argo-c1", "web", "g/c1", "web", "", "") + app("argo-c1", "kid", "g/c1", "kid", "apps: []", "root,parent")
	apps, err := Apps([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("want 2 applications, got %d", len(apps))
	}
	if apps[0].Call != nil || apps[0].Cluster != "g/c1" || apps[0].Name != "web" {
		t.Errorf("on-disk application parsed wrong: %+v", apps[0])
	}
	c := apps[1].Call
	if c == nil || c.Definition != "apps: []" || c.Base != "deployments/kid/.child" || strings.Join(c.Parents, ",") != "root,parent" {
		t.Errorf("call parsed wrong: %+v", c)
	}
}

func TestWalkRendersDownToTheLeaves(t *testing.T) {
	flat := app("argo-c1", "web", "c1", "web", "", "") + app("argo-c1", "bundle-a", "c1", "bundle-a", "def-a", "root")
	table := map[string]string{
		"bundle-a": app("argo-c2", "bundle-b", "c2", "bundle-b", "def-b", "root,bundle-a"),
		"bundle-b": configMap,
	}
	var seen []string
	res, err := Walk([]byte(flat), []string{"c1/web"}, fake(table, &seen))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, " ") != "c1/bundle-a c2/bundle-b" {
		t.Errorf("render order: %v", seen)
	}
	if got := strings.Join(res.Summary(), "|"); got != "c1/bundle-a (via root -> bundle-a)|c2/bundle-b (via root -> bundle-a -> bundle-b)" {
		t.Errorf("summary: %s", got)
	}
}

func TestWalkSameNameOnAnotherClusterIsNoCycle(t *testing.T) {
	// project bundle "shop" on mgmt with a child "shop" on another cluster
	flat := app("argo-mgmt", "shop", "mgmt", "shop", "def-project", "root")
	table := map[string]string{"shop": ""}
	table["shop"] = app("argo-eu1", "shop", "app/eu1", "shop", "def-workload", "root,shop")
	calls := 0
	render := func(c Call) ([]byte, error) {
		calls++
		if c.Definition == "def-project" {
			return []byte(table["shop"]), nil
		}
		return []byte(configMap), nil
	}
	if _, err := Walk([]byte(flat), nil, render); err != nil {
		t.Fatalf("same name with another context must be fine: %v", err)
	}
	if calls != 2 {
		t.Errorf("want 2 renders, got %d", calls)
	}
}

func TestWalkFailsOnTwoOwners(t *testing.T) {
	flat := app("argo-c1", "x", "c1", "x", "", "") + app("argo-c1", "bundle", "c1", "bundle", "def", "root")
	table := map[string]string{"bundle": app("argo-c1", "x", "c1", "x2", "def-x", "root,bundle")}
	var seen []string
	_, err := Walk([]byte(flat), nil, fake(table, &seen))
	if err == nil || !strings.Contains(err.Error(), "argo-c1/x comes from two places") {
		t.Fatalf("want two-owners error, got %v", err)
	}
}

func TestWalkFailsWhenAChildTakesADeploymentName(t *testing.T) {
	flat := app("argo-c1", "bundle", "c1", "bundle", "def", "root")
	table := map[string]string{"bundle": app("argo-c1", "web-copy", "c1", "web", "def-web", "root,bundle")}
	var seen []string
	_, err := Walk([]byte(flat), []string{"c1/web"}, fake(table, &seen))
	if err == nil || !strings.Contains(err.Error(), "child c1/web") || !strings.Contains(err.Error(), "deployment on disk") {
		t.Fatalf("want on-disk collision error, got %v", err)
	}
}

func TestWalkFailsOnACycle(t *testing.T) {
	// the child renders an Application that calls the same context again
	self := func(name string) string { return app("argo-c1", name, "c1", "loop", "def-loop", "root,loop") }
	flat := self("loop-1")
	n := 1
	render := func(Call) ([]byte, error) {
		n++
		return []byte(self(fmt.Sprintf("loop-%d", n))), nil
	}
	_, err := Walk([]byte(flat), nil, render)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestWalkFailsBeyondMaxDepth(t *testing.T) {
	flat := app("argo-c1", "d0", "c1", "d0", "def-0", "root")
	render := func(c Call) ([]byte, error) {
		var i int
		fmt.Sscanf(c.Deployment, "d%d", &i)
		return []byte(app("argo-c1", fmt.Sprintf("d%d", i+1), "c1", fmt.Sprintf("d%d", i+1), fmt.Sprintf("def-%d", i+1), "root")), nil
	}
	_, err := Walk([]byte(flat), nil, render)
	if err == nil || !strings.Contains(err.Error(), "nested more than") {
		t.Fatalf("want depth error, got %v", err)
	}
}

func TestRulesAcceptDeclaredAndStructureChildren(t *testing.T) {
	flat := app("argo-c1", "svc", "c1", "svc", "apps: [{template: parent}]", "root") +
		app("argo-c1", "svc-db", "c1", "svc-db", "apps: [{template: cnpg}]", "root")
	table := map[string]string{"svc": "", "svc-db": configMap}
	var seen []string
	rules := Rules{Check: true, Declared: []string{"c1/svc-db"}, StructureTemplate: "parent"}
	if _, err := WalkWith([]byte(flat), rules, fake(table, &seen)); err != nil {
		t.Fatalf("declared component and structure node must pass: %v", err)
	}
	if strings.Join(seen, " ") != "c1/svc c1/svc-db" {
		t.Errorf("renders: %v", seen)
	}
}

func TestRulesRejectUndeclaredChild(t *testing.T) {
	flat := app("argo-c1", "kid", "c1", "kid", "apps: [{template: app1}]", "root")
	var seen []string
	_, err := WalkWith([]byte(flat), Rules{Check: true, StructureTemplate: "parent"}, fake(map[string]string{"kid": configMap}, &seen))
	if err == nil || !strings.Contains(err.Error(), "child c1/kid") || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("want undeclared error, got %v", err)
	}
	if len(seen) != 0 {
		t.Errorf("an undeclared child must not render: %v", seen)
	}
}

func TestRulesStructureNodeWithMacrosIsNoStructureNode(t *testing.T) {
	r := Rules{Check: true, StructureTemplate: "parent"}
	if r.allowed(Call{Cluster: "c1", Deployment: "x", Definition: "apps: [{template: parent}]\ndeploymentTemplates: [{template: m}]"}) {
		t.Error("a definition with deploymentTemplates must not count as a pure structure node")
	}
	if r.allowed(Call{Cluster: "c1", Deployment: "x", Definition: "apps: [{template: parent}, {template: app1}]"}) {
		t.Error("a definition with other templates must not count as a pure structure node")
	}
	if !(Rules{}).allowed(Call{Cluster: "c1", Deployment: "x", Definition: "apps: [{template: app1}]"}) {
		t.Error("without Check every child is allowed")
	}
}
