package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	out, err := ResourceFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReleases(t *testing.T) {
	root := t.TempDir()
	write(t, root, "c1/d1/r1/chart/templates/a.yaml", "kind: A\n")
	// subchart: part of r1, not a release of its own
	write(t, root, "c1/d1/r1/chart/charts/sub/templates/b.yaml", "kind: B\n")
	// grouped cluster path
	write(t, root, "g/c2/d2/r2/chart/templates/a.yaml", "kind: A\n")
	// a chart that renders CRDs only
	write(t, root, "c1/d3/crds-only/chart/crds/crd.yaml", "kind: CustomResourceDefinition\n")
	// CRDs under templates/crds
	write(t, root, "c1/d4/r4/chart/templates/crds/crd.yaml", "kind: CustomResourceDefinition\n")

	got, err := Releases(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c1/d1/r1", "c1/d3/crds-only", "c1/d4/r4", "g/c2/d2/r2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Releases = %v, want %v", got, want)
	}
	if got, _ := Releases(filepath.Join(root, "missing")); got != nil {
		t.Fatalf("missing root: %v", got)
	}
}

func TestSplitRelease(t *testing.T) {
	c, d, r := SplitRelease("group1/cluster2/dep/rel")
	if c != "group1/cluster2" || d != "dep" || r != "rel" {
		t.Fatalf("got %q %q %q", c, d, r)
	}
}

func TestSplit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "r/chart/templates/patched_resources.yaml", `# Source: chart/templates/only-comment.yaml
# nothing rendered here
---
# Source: chart/templates/cm.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: Foo.Bar
  namespace: one
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: Foo.Bar
  namespace: two
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: "system:thing"
...
`)
	write(t, root, "r/chart/templates/single.yaml", "apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n")
	write(t, root, "r/chart/templates/broken.yaml", "a: [unclosed\n")
	if err := Split(root); err != nil {
		t.Fatal(err)
	}
	got := files(t, filepath.Join(root, "r"))
	want := []string{
		"chart/templates/broken.yaml",
		"chart/templates/clusterrole-system-thing.yaml",
		"chart/templates/configmap-foo-bar-2.yaml",
		"chart/templates/configmap-foo-bar.yaml",
		"chart/templates/secret-s.yaml",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	first, _ := os.ReadFile(filepath.Join(root, "r/chart/templates/configmap-foo-bar.yaml"))
	if !strings.HasPrefix(string(first), "# Source: chart/templates/only-comment.yaml\n") || !strings.Contains(string(first), "namespace: one") {
		t.Fatalf("comment-only document not carried into the next one:\n%s", first)
	}
	second, _ := os.ReadFile(filepath.Join(root, "r/chart/templates/configmap-foo-bar-2.yaml"))
	if !strings.Contains(string(second), "namespace: two") {
		t.Fatalf("collision suffix in document order:\n%s", second)
	}
	// idempotent: a second split changes nothing
	if err := Split(root); err != nil {
		t.Fatal(err)
	}
	if again := files(t, filepath.Join(root, "r")); !reflect.DeepEqual(again, want) {
		t.Fatalf("second split: %v", again)
	}
}

func TestMarker(t *testing.T) {
	// printf '%s' s3cretvalue | sha256sum | head -c 12
	if got := Marker("s3cretvalue"); got != "REDACTED:sha256:6930c86cacb4" {
		t.Fatalf("Marker = %s", got)
	}
}

func TestScrub(t *testing.T) {
	root, maps := t.TempDir(), t.TempDir()
	write(t, root, "c/d/r/chart/templates/x.yaml", `apiVersion: v1
kind: Secret
metadata:
  name: s
stringData:
  token: s3cretvalue
  done: REDACTED:sha256:9fab3a1ed1c3
data:
  empty: null
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: s3cretvalue
data:
  token: s3cretvalue
  port: 8080
`)
	write(t, root, "c/d/plain/chart/templates/y.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keep   # spacing kept\n")
	write(t, root, "c/d/orphan/chart/templates/z.yaml", "kind: Secret\n")
	write(t, maps, "c/d/r.json", `{"s3cretvalue":"REDACTED","8080":"REDACTED"}`)
	write(t, maps, "c/d/plain.json", `{}`)

	st, err := Scrub(root, maps, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, s := range st {
		lines = append(lines, s.String())
	}
	sort.Strings(lines)
	want := []string{"no-map c/d/orphan", "scrubbed c/d/plain", "scrubbed c/d/r"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("statuses = %v, want %v", lines, want)
	}
	data, _ := os.ReadFile(filepath.Join(root, "c/d/r/chart/templates/x.yaml"))
	out := string(data)
	if !strings.Contains(out, "token: "+Marker("s3cretvalue")) {
		t.Errorf("secret value not marker-redacted:\n%s", out)
	}
	if !strings.Contains(out, "done: REDACTED:sha256:9fab3a1ed1c3") {
		t.Errorf("existing marker changed:\n%s", out)
	}
	if strings.Count(out, "s3cretvalue") != 0 {
		t.Errorf("cleartext survived the replay:\n%s", out)
	}
	if !strings.Contains(out, "port: REDACTED") {
		t.Errorf("number scalar not replayed:\n%s", out)
	}
	plain, _ := os.ReadFile(filepath.Join(root, "c/d/plain/chart/templates/y.yaml"))
	if !strings.Contains(string(plain), "keep   # spacing kept") {
		t.Errorf("an unchanged file must keep its bytes:\n%s", plain)
	}
}

func TestDiff(t *testing.T) {
	if _, err := exec.LookPath("dyff"); err != nil {
		t.Skip("dyff not installed")
	}
	base, pr := t.TempDir(), t.TempDir()
	cm := func(v string) string {
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: " + v + "\n"
	}
	write(t, base, "c/d/same/chart/templates/a.yaml", cm("x"))
	write(t, pr, "c/d/same/chart/templates/a.yaml", cm("x"))
	write(t, base, "c/d/changed/chart/templates/a.yaml", cm("x"))
	write(t, pr, "c/d/changed/chart/templates/a.yaml", cm("y"))
	write(t, pr, "c/d/new/chart/templates/a.yaml", cm("x"))
	write(t, base, "c/d/gone/chart/templates/a.yaml", cm("x"))
	write(t, base, "c/d/nomap/chart/templates/a.yaml", cm("x"))
	write(t, pr, "c/d/nomap/chart/templates/a.yaml", cm("z"))
	write(t, base, "c/d/nomap-same/chart/templates/a.yaml", cm("x"))
	write(t, pr, "c/d/nomap-same/chart/templates/a.yaml", cm("x"))
	// formatting-only change: dyff sees no difference
	write(t, base, "c/d/format/chart/templates/a.yaml", cm("x"))
	write(t, pr, "c/d/format/chart/templates/a.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm}\ndata:\n  k: x\n")

	res, err := Diff(context.Background(), DiffOptions{
		BaselineDir: base, PRDir: pr,
		NoMap: map[string]bool{"c/d/nomap": true, "c/d/nomap-same": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusChanges {
		t.Fatalf("status %s", res.Status)
	}
	var got []string
	for _, r := range res.Releases {
		got = append(got, r.Path+":"+r.Change+":"+r.Suppressed)
	}
	want := []string{"c/d/changed::", "c/d/gone:removed:removed", "c/d/new:new:", "c/d/nomap::no-map"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("releases = %v, want %v", got, want)
	}
	if res.Releases[0].Resources[0].Label != "ConfigMap/cm" {
		t.Errorf("label %q", res.Releases[0].Resources[0].Label)
	}
	if !strings.HasPrefix(res.Releases[2].Resources[0].Text, "! + entire resource added:\n+ apiVersion: v1") {
		t.Errorf("new resource text:\n%s", res.Releases[2].Resources[0].Text)
	}
	if res.Resources() != 2 || res.Suppressed("removed") != 1 || res.Suppressed("no-map") != 1 {
		t.Errorf("counts: resources %d removed %d nomap %d", res.Resources(), res.Suppressed("removed"), res.Suppressed("no-map"))
	}

	empty, err := Diff(context.Background(), DiffOptions{BaselineDir: filepath.Join(base, "x"), PRDir: filepath.Join(pr, "y")})
	if err != nil || empty.Status != StatusEmpty {
		t.Fatalf("empty: %v %v", empty.Status, err)
	}
}
