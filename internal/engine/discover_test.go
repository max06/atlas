package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkfiles(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, f)
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("apps: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoverLevelsAndOverride(t *testing.T) {
	root := t.TempDir()
	mkfiles(t, root,
		"deployments/apps/shared/deployment.yaml",
		"deployments/apps/both/deployment.yaml",
		"deployments/grp/apps/both/deployment.yaml", // group overrides global
		"deployments/grp/c1/apps/own/deployment.yaml",
		"deployments/grp/c1/cluster.values.yaml", // a file in a cluster dir is no child
		"deployments/solo/apps/.keep",
		"deployments/grp/c2/apps/", // empty apps: a cluster without own deployments
	)
	pairs, err := Discover(Layout{Root: root, Deployments: "deployments"}, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range pairs {
		got = append(got, p.Cluster+"|"+p.ClusterName+"|"+p.ClusterGroup+"|"+p.DeploymentName+"|"+strings.TrimPrefix(p.DeploymentPath, root))
	}
	want := []string{
		"grp/c1|c1|grp|both|/deployments/grp/apps/both/deployment.yaml",
		"grp/c1|c1|grp|own|/deployments/grp/c1/apps/own/deployment.yaml",
		"grp/c1|c1|grp|shared|/deployments/apps/shared/deployment.yaml",
		"grp/c2|c2|grp|both|/deployments/grp/apps/both/deployment.yaml",
		"grp/c2|c2|grp|shared|/deployments/apps/shared/deployment.yaml",
		"solo|solo||both|/deployments/apps/both/deployment.yaml",
		"solo|solo||shared|/deployments/apps/shared/deployment.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// the filter: exact cluster paths, deployment names
	pairs, err = Discover(Layout{Root: root, Deployments: "deployments"}, Filter{Clusters: []string{"grp/c1", "c2"}, Deployments: []string{"own", "both"}})
	if err != nil || len(pairs) != 2 || pairs[0].DeploymentName != "both" || pairs[1].DeploymentName != "own" {
		t.Errorf("filtered: %+v, %v", pairs, err)
	}
}

func TestDiscoverErrors(t *testing.T) {
	if _, err := Discover(Layout{Root: "relative", Deployments: "d"}, Filter{}); err == nil || err.Error() != "No valid cwd given" {
		t.Errorf("relative root: %v", err)
	}
	root := t.TempDir()
	mkfiles(t, root, "d/a/x/apps/", "d/b/x/apps/")
	_, err := Discover(Layout{Root: root, Deployments: "d"}, Filter{Clusters: []string{"a/x"}})
	if err == nil || !strings.Contains(err.Error(), `duplicate leaf cluster name "x" used by: a/x, b/x`) {
		t.Errorf("duplicate leaf despite the filter: %v", err)
	}
}

func TestGlobCornerCases(t *testing.T) {
	root := t.TempDir()
	mkfiles(t, root, "a/apps/x", "a/b/apps/", "a/f", "c/apps")
	// ** finds every "apps" entry, files too; recursion only into directories
	got, err := glob(root + "/**/apps")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root + "/a/apps", root + "/a/b/apps", root + "/c/apps"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("**: %v", got)
	}
	// a wildcard part matches files too, but the next part needs a directory
	if got, err = glob(root + "/a/*/apps"); err != nil || len(got) != 1 || got[0] != root+"/a/b/apps" {
		t.Errorf("*/apps: %v, %v", got, err)
	}
	// an empty part is skipped, a "." part is kept in the path
	if got, err = glob(root + "//a/./*/apps"); err != nil || len(got) != 1 || got[0] != root+"/a/./b/apps" {
		t.Errorf("// and ./: %v, %v", got, err)
	}
}
