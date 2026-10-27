package promote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/max06/atlas/internal/gitrev"
)

// fixtureRepo builds a repository from the test tree (native engine, so no
// helmfile is needed): commit base, branch "to" (base + a file only on to +
// the conflicting side), branch "from" with the scenarios.
func fixtureRepo(t *testing.T) *gitrev.Repo {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	tests := filepath.Join(filepath.Dir(file), "..", "..", "tests")
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@localhost", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@localhost")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
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
	appendTo := func(rel, body string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		write(rel, string(data)+body)
	}
	for _, sub := range []string{"templates", "deployments", "charts"} {
		if out, err := exec.Command("cp", "-r", filepath.Join(tests, sub), dir).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v\n%s", err, out)
		}
	}
	write("atlas.yaml", "apiVersion: atlas.io/v1alpha1\nkind: Root\nspec:\n  engine: \"0.14\"\n  native: true\n")
	git("init", "-q", "-b", "base")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")

	git("checkout", "-q", "-b", "to")
	write("TO-ONLY.md", "only on to\n")
	write("docs/same.md", "same on both\n")
	write("deployments/group1/cluster3/apps/deployment4/values.yaml", "d4: fromTo\n")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "to")

	git("checkout", "-q", "-b", "from", "base")
	// A: template app1 (deployment1, 2, 7, 12)
	appendTo("templates/app1/values.yaml.gotmpl", "promoteA: changed\n")
	// B: template app-novals-b (deployment7 only, which also has app1)
	write("templates/app-novals-b/helmfile.yaml.gotmpl", "releases:\n  - name: app-novals-b\n    chart: ../../charts/chart1\n    namespace: test\n    values:\n      - promoteB: changed\n")
	// C: deployment12 (app1): the same release as A
	appendTo("deployments/cluster1/apps/deployment12/values.yaml", "promoteC: changed\n")
	// D: deployment3 on its own
	write("deployments/cluster1/apps/deployment3/values.yaml", "promoteD: changed\n")
	// E: deployment4, added differently on both branches
	write("deployments/group1/cluster3/apps/deployment4/values.yaml", "d4: fromFrom\n")
	// F: the same content on both branches
	write("docs/same.md", "same on both\n")
	// H: a new template and a new deployment on a skipped cluster
	write("templates/app-new/helmfile.yaml.gotmpl", "releases:\n  - name: app-new\n    chart: ../../charts/chart1\n    namespace: test\n")
	write("deployments/group1/cluster3/apps/deployment-new/deployment.yaml", "apps:\n  - template: app-new\n")
	// J: a README in a template: deployment9 renders unchanged
	write("templates/app-named/README.md", "docs only\n")
	// I: template app-inlinevals switches to a new local chart: the template
	// does not render without the chart, the chart alone changes nothing
	data, err := os.ReadFile(filepath.Join(dir, "templates/app-inlinevals/helmfile.yaml.gotmpl"))
	if err != nil {
		t.Fatal(err)
	}
	write("templates/app-inlinevals/helmfile.yaml.gotmpl", strings.Replace(string(data), "/charts/chart1", "/charts/chart-promote", 1))
	if out, err := exec.Command("cp", "-r", filepath.Join(dir, "charts/chart1"), filepath.Join(dir, "charts/chart-promote")).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "from")
	return &gitrev.Repo{Dir: dir}
}

func ids(p *Plan) map[string]string {
	out := map[string]string{}
	for _, u := range p.Units {
		out[u.ID] = u.Status
	}
	return out
}

func TestPlan(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := fixtureRepo(t)
	ctx := context.Background()
	o := Options{Repo: repo, From: "from", To: "to", Entry: "helmfile.yaml.gotmpl", SkipClusters: []string{"cluster3"}, Work: filepath.Join(t.TempDir(), "w")}
	p, err := Make(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"deployment:cluster1/apps/deployment12 + template:app-novals-b + template:app1": Applies,
		"deployment:cluster1/apps/deployment3":                                          Applies,
		"deployment:group1/cluster3/apps/deployment4":                                   Conflicts,
		"deployment:group1/cluster3/apps/deployment-new + template:app-new":             Applies,
		"chart:charts/chart-promote + template:app-inlinevals":                          Applies,
		"template:app-named": Applies,
	}
	if got := ids(p); !reflect.DeepEqual(got, want) {
		t.Errorf("units:\n got %v\nwant %v", got, want)
	}
	if p.Present != 1 || !reflect.DeepEqual(p.ToOnly, []string{"TO-ONLY.md"}) {
		t.Errorf("present %d, to only %v", p.Present, p.ToOnly)
	}
	for _, u := range p.Units {
		switch u.ID {
		case "deployment:group1/cluster3/apps/deployment4":
			if !reflect.DeepEqual(u.Conflicts, []string{"deployments/group1/cluster3/apps/deployment4/values.yaml"}) || !u.NoEffect {
				t.Errorf("conflict unit: %+v", u)
			}
		case "deployment:group1/cluster3/apps/deployment-new + template:app-new":
			if !u.NoEffect || !reflect.DeepEqual(u.NewPairs, []string{"group1/cluster3|deployment-new"}) {
				t.Errorf("new deployment on a skipped cluster: %+v", u)
			}
		case "deployment:cluster1/apps/deployment3":
			if u.NoEffect || !reflect.DeepEqual(u.Pairs, []string{"cluster1|deployment3"}) {
				t.Errorf("deployment3: %+v", u)
			}
		}
	}

	// the split separates B (another release of deployment7) from A and C
	// (the same release of deployment12)
	o.Split = true
	p, err = Make(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]string{
		"deployment:cluster1/apps/deployment12 + template:app1":             Applies,
		"template:app-novals-b":                                             Applies,
		"deployment:cluster1/apps/deployment3":                              Applies,
		"deployment:group1/cluster3/apps/deployment4":                       Conflicts,
		"deployment:group1/cluster3/apps/deployment-new + template:app-new": Applies,
		"chart:charts/chart-promote + template:app-inlinevals":              Applies,
		"template:app-named":                                                Applies,
	}
	if got := ids(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("units after the split:\n got %v\nwant %v", got, want)
	}
	for _, u := range p.Units {
		if u.ID == "deployment:cluster1/apps/deployment12 + template:app1" {
			if u.Split == nil || len(u.Split.Links) != 1 || !strings.Contains(u.Split.Links[0], "both change cluster1/deployment12/app1") {
				t.Errorf("split evidence: %+v", u.Split)
			}
		}
		if u.ID == "chart:charts/chart-promote + template:app-inlinevals" {
			if u.Split == nil || len(u.Split.Links) != 1 || !strings.Contains(u.Split.Links[0], "template:app-inlinevals: does not render alone") {
				t.Errorf("split evidence: %+v", u.Split)
			}
		}
		if u.ID == "template:app-named" {
			if !u.NoEffect || u.Split == nil || u.Split.Changed == nil || len(u.Split.Changed) != 0 {
				t.Errorf("README only: %+v %+v", u, u.Split)
			}
		}
		if u.ID == "deployment:cluster1/apps/deployment3" {
			if u.NoEffect || u.Split == nil || !reflect.DeepEqual(u.Split.Changed, []string{"cluster1/deployment3/app-novals"}) {
				t.Errorf("deployment3 render: %+v", u.Split)
			}
		}
		if u.ID == "template:app-novals-b" {
			if u.Split == nil || !reflect.DeepEqual(u.Split.Releases["template:app-novals-b"], []string{"cluster1/deployment7/app-novals-b"}) {
				t.Errorf("split evidence: %+v", u.Split)
			}
		}
	}
	md := Markdown(p)
	t.Log(Text(p))
	for _, s := range []string{
		"- [ ] **template:app-novals-b**",
		"<!-- atlas-promote-unit: template:app-novals-b -->",
		"- **deployment:group1/cluster3/apps/deployment4** —",
		"conflicts in: `deployments/group1/cluster3/apps/deployment4/values.yaml`",
		"### Ready: no effect on deployments of `to`",
		"**template:app-named** — 1 file(s); renders unchanged on `to` (1 deployment(s): cluster1/deployment9)",
		"1 release(s) change on `to`: cluster1/deployment3/app-novals",
	} {
		if !strings.Contains(md, s) {
			t.Errorf("markdown misses %q:\n%s", s, md)
		}
	}
}

func TestAtomID(t *testing.T) {
	templates := []string{"peopleware-apps/iwfm", "peopleware-apps/iwfm-api", "redis"}
	charts := []string{"charts/mychart"}
	for path, want := range map[string]string{
		"templates/peopleware-apps/iwfm-api/image.yaml":      "template:peopleware-apps/iwfm-api",
		"templates/peopleware-apps/iwfm/workloads.yaml":      "template:peopleware-apps/iwfm",
		"templates/peopleware-apps/_helpers.tpl":             "template:peopleware-apps",
		"templates/README.md":                                "other:templates",
		"templates/unknown/x.yaml":                           "template:unknown",
		"deployments/infra/alpha/apps/redis/deployment.yaml": "deployment:infra/alpha/apps/redis",
		"deployments/infra/alpha/apps/redis/sub/values.yaml": "deployment:infra/alpha/apps/redis",
		"deployments/infra/alpha/cluster.values.yaml.gotmpl": "hierarchy:infra/alpha",
		"deployments/global.values.yaml":                     "hierarchy:global",
		"charts/mychart/templates/a.yaml":                    "chart:charts/mychart",
		"docs/adr/0001.md":                                   "other:docs",
		"AGENTS.md":                                          "other:/",
	} {
		if got := atomID(path, "deployments", "templates", templates, charts); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
}
