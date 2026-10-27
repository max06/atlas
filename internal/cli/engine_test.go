package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEngineFromAtlasYAML(t *testing.T) {
	dir := t.TempDir()
	g := &globalFlags{dir: dir}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "atlas.yaml"), []byte("apiVersion: atlas.io/v1alpha1\nkind: Root\nspec:\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name, flag string, want, wantErr bool) {
		t.Helper()
		got, err := (&engineFlags{name: flag}).native(g)
		if got != want || (err != nil) != wantErr {
			t.Errorf("%s: native=%v err=%v, want native=%v err=%v", name, got, err, want, wantErr)
		}
	}
	check("no atlas.yaml", "", false, false)
	write("  engine: \"0.14\"\n  native: true\n")
	check("spec.native", "", true, false)
	check("flag wins", "helmfile", false, false)
	check("unknown flag", "templates", false, true)
	write("  engine: \"0.14\"\n")
	check("pin without native", "", false, false)
	check("flag native", "native", true, false)
	write("  engine: \"0.13\"\n  native: true\n")
	check("invalid atlas.yaml", "", false, true)
}
