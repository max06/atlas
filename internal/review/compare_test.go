package review

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompareGroupsFilesByRelease(t *testing.T) {
	base, head := t.TempDir(), t.TempDir()
	// grouped cluster: the release is the segment after <cluster>/<deployment>/
	put(t, base, "g1/c1/web/web/chart/templates/deploy.yaml", "replicas: 1")
	put(t, head, "g1/c1/web/web/chart/templates/deploy.yaml", "replicas: 2")
	put(t, base, "g1/c1/web/web/chart/templates/svc.yaml", "same")
	put(t, head, "g1/c1/web/web/chart/templates/svc.yaml", "same")
	put(t, base, "c2/db/db/chart/templates/x.yaml", "gone")
	put(t, head, "c2/db/new/chart/templates/x.yaml", "new")
	put(t, base, "c2/db/same/chart/templates/x.yaml", "same")
	put(t, head, "c2/db/same/chart/templates/x.yaml", "same")

	got, err := Compare(base, head, []string{"g1/c1|web", "c2|db"})
	if err != nil {
		t.Fatal(err)
	}
	want := []ReleaseChange{
		{Release: "c2/db/db", Kind: "removed", Files: []string{"chart/templates/x.yaml"}},
		{Release: "c2/db/new", Kind: "added", Files: []string{"chart/templates/x.yaml"}},
		{Release: "g1/c1/web/web", Kind: "changed", Files: []string{"chart/templates/deploy.yaml"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestCompareMissingSideIsAllAdded(t *testing.T) {
	head := t.TempDir()
	put(t, head, "c/d/r/x.yaml", "x")
	got, err := Compare(filepath.Join(t.TempDir(), "absent"), head, []string{"c|d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "added" || got[0].Release != "c/d/r" {
		t.Fatalf("got %+v", got)
	}
}
