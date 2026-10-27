package renderer

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestPreparedOracle compares Prepared with `helmfile build` on a generated
// root (ATLAS_PREPARE_ORACLE=<root helmfile.yaml>; needs helmfile).
func TestPreparedOracle(t *testing.T) {
	root := os.Getenv("ATLAS_PREPARE_ORACLE")
	if root == "" {
		t.Skip("ATLAS_PREPARE_ORACLE not set")
	}
	r, err := LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("helmfile", "-f", root, "build")
	cmd.Dir = filepath.Dir(root)
	cmd.Env = append(os.Environ(), "HELMFILE_UPGRADE_NOTICE_DISABLED=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	type rel struct {
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Chart       string            `yaml:"chart"`
		Version     string            `yaml:"version"`
		KubeContext string            `yaml:"kubeContext"`
		Labels      map[string]string `yaml:"labels"`
	}
	built := map[string][]rel{}
	dec := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var st struct {
			FilePath string `yaml:"filepath"`
			Releases []rel  `yaml:"releases"`
		}
		err := dec.Decode(&st)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, seen := built[st.FilePath]; !seen {
			built[st.FilePath] = st.Releases
		}
	}
	n := 0
	for _, st := range r.States {
		want := built[filepath.Base(st.File)]
		got := st.Prepared()
		if len(want) != len(got) {
			t.Errorf("%s: %d releases, helmfile %d", st.File, len(got), len(want))
			continue
		}
		for i := range got {
			g := rel{Name: got[i].Name, Namespace: got[i].Namespace, Chart: got[i].Chart, Version: got[i].Version, KubeContext: got[i].KubeContext, Labels: got[i].Labels}
			if len(g.Labels) == 0 {
				g.Labels = nil
			}
			if !reflect.DeepEqual(g, want[i]) {
				t.Errorf("%s release %d:\n native  %+v\n helmfile %+v", st.File, i, g, want[i])
			}
			n++
		}
	}
	t.Logf("%d states, %d releases compared", len(r.States), n)
}
