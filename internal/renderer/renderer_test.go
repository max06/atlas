package renderer

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelector(t *testing.T) {
	sel, err := ParseSelector("cluster=a/b,deploymentName!=x")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		labels map[string]string
		want   bool
	}{
		{map[string]string{"cluster": "a/b", "deploymentName": "y"}, true},
		{map[string]string{"cluster": "a/b", "deploymentName": "x"}, false},
		{map[string]string{"cluster": "a/b"}, true}, // != holds for a missing label
		{map[string]string{"deploymentName": "y"}, false},
	} {
		if got := sel.matches(c.labels); got != c.want {
			t.Errorf("%v: got %v, want %v", c.labels, got, c.want)
		}
	}
	if _, err := ParseSelector("novalue"); err == nil {
		t.Error("a label without = must fail")
	}
}

func TestSetOptions(t *testing.T) {
	o, err := setOptions(Release{
		Set: []SetValue{
			{Name: "a", Value: "x,y"},
			{Name: "b", File: "/f.txt"},
			{Name: "c", Values: []any{"1", 2}},
		},
		SetString: []SetValue{
			{Name: "d", Value: "{v}"},
			{Name: "e", File: "/ignored.txt"}, // helmfile's setStringFlags ignores file
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`a=x\,y`, "c={1,2}"}; !reflect.DeepEqual(o.Values, want) {
		t.Errorf("Values = %q, want %q", o.Values, want)
	}
	if want := []string{`d=\{v\}`}; !reflect.DeepEqual(o.StringValues, want) {
		t.Errorf("StringValues = %q, want %q", o.StringValues, want)
	}
	if want := []string{"b=/f.txt"}; !reflect.DeepEqual(o.FileValues, want) {
		t.Errorf("FileValues = %q, want %q", o.FileValues, want)
	}
	if _, err := setOptions(Release{Set: []SetValue{{Name: "s", Value: "ref+sops://x"}}}); err == nil {
		t.Error("a vals reference must fail")
	}
	flags, err := chartifySetFlags([]SetValue{{Name: "a", Value: "1"}, {Name: "b", File: "/f"}, {Name: "c", Value: "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--set", "a=1", "--set-file", "b=/f", "--set", "c=2"}; !reflect.DeepEqual(flags, want) {
		t.Errorf("chartify flags = %q, want %q", flags, want)
	}
}

func TestReleaseID(t *testing.T) {
	for _, c := range []struct {
		rel  Release
		want string
	}{
		{Release{Name: "r"}, "r"},
		{Release{Name: "r", Namespace: "ns"}, "ns/r"},
		{Release{Name: "r", KubeContext: "kc"}, "kc//r"},
		{Release{Name: "r", Namespace: "ns", KubeContext: "kc"}, "kc/ns/r"},
	} {
		if got := releaseID(c.rel); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestOutputDir(t *testing.T) {
	st := &State{File: "states/0001.yaml", Env: map[string]any{"atlas": map[string]any{"deployment": map[string]any{"cluster": "g/c", "deploymentName": "d"}}}}
	rel := Release{Name: "rel"}
	got, err := st.outputDir("/root", "/out", "{{.OutputDir}}/{{.Environment.Values.atlas.deployment.cluster}}/{{.Environment.Values.atlas.deployment.deploymentName}}/{{.Release.Name}}", rel)
	if err != nil || got != "/out/g/c/d/rel" {
		t.Errorf("atlas template: %q, %v", got, err)
	}
	// helmfile default: <base>-<sha1(abs state path)[:8]>-<release>
	got, err = st.outputDir("/root", "/out", "", rel)
	if err != nil || got != "/out/0001-5f8b840c-rel" {
		t.Errorf("default template: %q, %v", got, err)
	}
}

func TestLoadStateRefusesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"field.yaml": "releases:\n  - name: r\n    chart: c\n    hooks: []\n",
		"state.yaml": "helmfiles:\n  - path: other.yaml\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadState(dir, name); err == nil {
			t.Errorf("%s: must fail", name)
		}
	}
}

func TestPreparedReleaseTemplates(t *testing.T) {
	st := &State{
		Env:          map[string]any{"atlas": map[string]any{"deployment": map[string]any{"clusterName": "c1"}}},
		CommonLabels: map[string]string{"team": "a"},
		Releases: []Release{{
			Name: "r", Chart: "repo/chart", Namespace: "{{ .Values.atlas.deployment.clusterName }}-ns",
			Labels: map[string]string{"ns": "{{ .Release.Namespace }}"},
		}},
	}
	got, err := st.Prepared("")
	if err != nil {
		t.Fatal(err)
	}
	// the fixpoint: the label sees the rendered namespace in the next round
	want := map[string]string{"team": "a", "ns": "c1-ns", "name": "r", "namespace": "c1-ns", "chart": "chart"}
	if got[0].Namespace != "c1-ns" || !reflect.DeepEqual(got[0].Labels, want) {
		t.Errorf("got %+v", got[0])
	}
}
