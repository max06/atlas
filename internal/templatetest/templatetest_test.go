package templatetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	tests := filepath.Join(filepath.Dir(file), "..", "..", "tests")
	dir := t.TempDir()
	for _, sub := range []string{"templates", "deployments", "charts"} {
		if out, err := exec.Command("cp", "-r", filepath.Join(tests, sub), dir).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v\n%s", err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		f := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("atlas.yaml", "apiVersion: atlas.io/v1alpha1\nkind: Root\nspec:\n  engine: \"0.14\"\n  native: true\n")
	write("templates/app1/tests/app1_test.yaml", `suite: app1
tests:
  - it: renders one ConfigMap with the test values
    values:
      - promoteTest: fromTest
    asserts:
      - hasDocuments:
          count: 1
        template: templates/configmap.yaml
      - isKind:
          of: ConfigMap
        template: "templates/*.yaml"
      - equal:
          path: data.values.promoteTest
          value: fromTest
      - equal:
          path: data.values.atlas.deployment.clusterName
          value: cluster1
  - it: fails on a wrong name
    asserts:
      - equal:
          path: metadata.name
          value: not-the-name
  - it: an unknown release has no documents
    release: nothing
    asserts:
      - hasDocuments:
          count: 0
`)
	original, err := os.ReadFile(filepath.Join(dir, "deployments/cluster1/apps/deployment1/deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := Run(context.Background(), Options{Dir: dir, Entry: "helmfile.yaml.gotmpl", Work: filepath.Join(t.TempDir(), "w")})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results: %+v", results)
	}
	if !results[0].Passed || results[0].Pair != "cluster1|deployment1" {
		t.Errorf("test 1: %+v", results[0])
	}
	if results[1].Passed || !strings.Contains(strings.Join(results[1].Failures, "\n"), "not-the-name") {
		t.Errorf("test 2 must fail with the expected value: %+v", results[1])
	}
	if !results[2].Passed {
		t.Errorf("test 3: %+v", results[2])
	}
	// the tree itself is untouched (the tests run on a copy)
	if now, _ := os.ReadFile(filepath.Join(dir, "deployments/cluster1/apps/deployment1/deployment.yaml")); string(now) != string(original) {
		t.Error("the deployment.yaml of the tree changed")
	}
}
