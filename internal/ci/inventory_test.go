package ci

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/max06/atlas/internal/renderer"
)

func TestChartSourceResolvesAliasesOCIAndLocalPaths(t *testing.T) {
	tree := filepath.FromSlash("/work/repo")
	repos := []renderer.Repository{
		{Name: "jetstack", URL: "https://charts.jetstack.io"},
		{Name: "private", URL: "https://user:secret@charts.example.com/stable?token=x"},
		{Name: "kgw", URL: "cr.kgateway.dev/kgateway-dev/charts", OCI: true},
	}
	cases := []struct{ chart, name, repo string }{
		{"jetstack/cert-manager", "cert-manager", "https://charts.jetstack.io"},
		{"private/app", "app", "https://charts.example.com/stable"},
		{"kgw/kgateway", "kgateway", "oci://cr.kgateway.dev/kgateway-dev/charts"},
		{"oci://registry.example.com/charts/rook-ceph", "rook-ceph", "oci://registry.example.com/charts"},
		{"oci://user:pw@registry.example.com/charts/x", "x", "oci://registry.example.com/charts"},
		{filepath.FromSlash("/work/repo/templates/cert-manager/crds"), "templates/cert-manager/crds", ""},
		{filepath.FromSlash("/elsewhere/runner/charts/thing"), "thing", ""},
		{"unknown/chart", "unknown/chart", ""},
	}
	for _, c := range cases {
		name, repo := chartSource(c.chart, repos, tree)
		if name != c.name || repo != c.repo {
			t.Errorf("chartSource(%q) = %q, %q; want %q, %q", c.chart, name, repo, c.name, c.repo)
		}
	}
}

func TestInventoryTreeImagesAndCRDs(t *testing.T) {
	tree := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("infra/zulu/app/rel/deploy.yaml", `apiVersion: apps/v1
kind: Deployment
metadata: {name: app}
spec:
  template:
    spec:
      initContainers: [{name: init, image: "busybox:1.37"}]
      containers: [{name: app, image: "ghcr.io/example/app:v1.2.3"}]
---
apiVersion: ceph.rook.io/v1
kind: CephCluster
metadata: {name: ceph}
spec:
  cephVersion: {image: "quay.io/ceph/ceph:v19.2.6"}
  dashboard: {image: {repository: "not-a-string-image"}}
`)
	write("infra/zulu/app/rel/crd.yaml", `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: foos.example.com}
spec:
  group: example.com
  names: {kind: Foo}
  versions:
    - name: v1alpha1
      served: true
      storage: false
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          properties:
            image: {type: string, description: "the image"}
`)
	write("infra/alpha/app/rel/deploy.yaml", `kind: Deployment
metadata: {name: app}
spec: {template: {spec: {containers: [{name: app, image: "ghcr.io/example/app:v1.2.3"}]}}}
`)
	images, crds, err := inventoryTree(tree, []string{"infra/zulu|app", "infra/alpha|app"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, i := range images {
		got[i.Image] = i.Resources
	}
	want := map[string][]string{
		"busybox:1.37":               {"infra/zulu/app/rel: Deployment/app"},
		"ghcr.io/example/app:v1.2.3": {"infra/alpha/app/rel: Deployment/app", "infra/zulu/app/rel: Deployment/app"},
		"quay.io/ceph/ceph:v19.2.6":  {"infra/zulu/app/rel: CephCluster/ceph"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("images:\n got %v\nwant %v", got, want)
	}
	if len(crds) != 1 {
		t.Fatalf("crds: %+v", crds)
	}
	c := crds[0]
	wantVersions := []InventoryCRDVersion{{"v1alpha1", true, false}, {"v1", true, true}}
	if c.Name != "foos.example.com" || c.Group != "example.com" || c.Kind != "Foo" || c.Release != "infra/zulu/app/rel" ||
		!reflect.DeepEqual(c.Versions, wantVersions) {
		t.Errorf("crd: %+v", c)
	}
}

func TestInventoryTreeEmpty(t *testing.T) {
	images, crds, err := inventoryTree(filepath.Join(t.TempDir(), "missing"), nil)
	if err != nil || images == nil || crds == nil || len(images)+len(crds) != 0 {
		t.Errorf("empty tree: %v %v %v", images, crds, err)
	}
}
