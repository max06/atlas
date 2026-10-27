package snapshot

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMapsAndRedactTree(t *testing.T) {
	root, mapDir := t.TempDir(), t.TempDir()
	write := func(rel, body string) {
		f := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// a release with a map, a release without secrets (no map), a Secret
	write("g/c1/app/web/chart/templates/cm.yaml", "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: web\ndata:\n  password: hunter2\n")
	write("g/c1/app/plain/chart/templates/cm.yaml", "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: plain\ndata:\n  color: blue\n")
	write("g/c1/app/sec/chart/templates/secret.yaml", "---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: sec\nstringData:\n  token: abc\n")
	n, err := WriteMaps(mapDir, []Maps{{Cluster: "g/c1", DeploymentName: "app", Releases: map[string]map[string]any{"web": {"hunter2": "REDACTED"}, "sec": nil}}})
	if err != nil || n != 2 {
		t.Fatalf("WriteMaps: %d %v", n, err)
	}
	failed, err := RedactTree(root, mapDir, []string{"g/c1|app"}, io.Discard)
	if err != nil || len(failed) != 0 {
		t.Fatalf("RedactTree: %v %v", failed, err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("g/c1/app/web/chart/templates/cm.yaml"); strings.Contains(got, "hunter2") || !strings.Contains(got, "REDACTED") {
		t.Errorf("map not applied:\n%s", got)
	}
	if got := read("g/c1/app/plain/chart/templates/cm.yaml"); !strings.Contains(got, "blue") {
		t.Errorf("release without secrets changed:\n%s", got)
	}
	if got := read("g/c1/app/sec/chart/templates/secret.yaml"); strings.Contains(got, "abc") || !strings.Contains(got, MarkerPrefix) {
		t.Errorf("Secret value not hashed:\n%s", got)
	}
	// the backfill wrote an empty map for the release without secrets
	if b, err := os.ReadFile(filepath.Join(mapDir, "g/c1/app/plain.json")); err != nil || strings.TrimSpace(string(b)) != "{}" {
		t.Errorf("backfill: %q %v", b, err)
	}

	// fail closed: a map directory below a release path marks it unscrubbed,
	// and its files leave the tree
	if err := os.MkdirAll(filepath.Join(mapDir, "g/c1/app/web/nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	failed, err = RedactTree(root, mapDir, []string{"g/c1|app"}, io.Discard)
	if err != nil || len(failed) != 1 || failed[0].Release != "g/c1/app/web" {
		t.Fatalf("fail closed: %v %v", failed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "g/c1/app/web")); !os.IsNotExist(err) {
		t.Errorf("unscrubbed release left in the tree: %v", err)
	}
	if _, err := WriteMaps(mapDir, []Maps{{DeploymentName: "app"}}); err == nil {
		t.Error("a carrier without cluster must fail")
	}
}
