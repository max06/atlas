package ci

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/max06/atlas/internal/snapshot"
)

func TestWriteExportFullDiffNoSuppressedContentRedactedTreeOnly(t *testing.T) {
	tmp := t.TempDir()
	pr := filepath.Join(tmp, "pr")
	if err := os.MkdirAll(filepath.Join(pr, "cluster1", "deployment1", "app1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pr, "cluster1", "deployment1", "app1", "configmap.yaml"), []byte("kind: ConfigMap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a symlink in the tree is not followed (it could point outside)
	outside := filepath.Join(tmp, "outside.txt")
	if err := os.WriteFile(outside, []byte("not part of the tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(pr, "cluster1", "link.txt")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "export")
	// a stale file of an earlier run must not survive
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	long := strings.Repeat("+ line\n", 5000) + "+ last"
	res := snapshot.DiffResult{Status: snapshot.StatusChanges, Releases: []snapshot.ReleaseDiff{
		{Path: "cluster1/deployment1/app1", Resources: []snapshot.ResourceDiff{{Label: "ConfigMap/app1", Text: long}}},
		{Path: "group/cluster2/deployment2/app2", Change: "removed", Suppressed: "removed",
			Resources: []snapshot.ResourceDiff{{Label: "Secret/app2", Text: "must not be exported"}}},
	}}
	err := WriteExport(ExportInput{
		Dir: dir, PRDir: pr, Base: "b1", Head: "h1", PRStatus: "success", BaselineStatus: "success",
		RenderMode: "subset", Pairs: []string{"cluster1|deployment1"}, Result: res,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("stale file survived: %v", err)
	}

	var got ExportDiff
	body, err := os.ReadFile(filepath.Join(dir, "diff.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != ExportVersion || got.Status != "changes" || got.Base != "b1" || got.Head != "h1" || got.RenderMode != "subset" {
		t.Errorf("header: %+v", got)
	}
	if len(got.Releases) != 2 {
		t.Fatalf("releases: %+v", got.Releases)
	}
	r0 := got.Releases[0]
	if r0.Cluster != "cluster1" || r0.Deployment != "deployment1" || r0.Release != "app1" {
		t.Errorf("path split: %+v", r0)
	}
	if len(r0.Resources) != 1 || r0.Resources[0].Diff != long {
		t.Errorf("the diff text is cut or missing (%d resources)", len(r0.Resources))
	}
	r1 := got.Releases[1]
	if r1.Suppressed != "removed" || len(r1.Resources) != 0 || strings.Contains(string(body), "must not be exported") {
		t.Errorf("suppressed release exported with content: %+v", r1)
	}
	if r1.Cluster != "group/cluster2" {
		t.Errorf("nested cluster path: %q", r1.Cluster)
	}

	if data, err := os.ReadFile(filepath.Join(dir, "merge-result", "cluster1", "deployment1", "app1", "configmap.yaml")); err != nil || string(data) != "kind: ConfigMap\n" {
		t.Errorf("merge-result tree: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "merge-result", "cluster1", "link.txt")); !os.IsNotExist(err) {
		t.Errorf("symlink was copied: %v", err)
	}
}

func TestWriteExportWithoutTreeWritesDiffOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "export")
	err := WriteExport(ExportInput{Dir: dir, PRDir: filepath.Join(dir, "missing"), Result: snapshot.DiffResult{Status: snapshot.StatusEmpty}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var got ExportDiff
	body, _ := os.ReadFile(filepath.Join(dir, "diff.json"))
	if err := json.Unmarshal(body, &got); err != nil || got.Status != "empty" || got.Releases == nil || got.RenderMode != "full" {
		t.Errorf("diff.json: %s (%v)", body, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "merge-result")); !os.IsNotExist(err) {
		t.Errorf("merge-result without a tree: %v", err)
	}
}

func TestWriteExportListsEachPairOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "export")
	pairs := []string{"infra/zulu|app", "infra/alpha|app", "infra/zulu|app", "infra/alpha|app"} // both sides, as unionPairs gives them
	if err := WriteExport(ExportInput{Dir: dir, Pairs: pairs, Result: snapshot.DiffResult{Status: snapshot.StatusNoChanges}}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var got ExportDiff
	body, _ := os.ReadFile(filepath.Join(dir, "diff.json"))
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if want := []string{"infra/alpha|app", "infra/zulu|app"}; !reflect.DeepEqual(got.Pairs, want) {
		t.Errorf("pairs = %v, want %v", got.Pairs, want)
	}
}
