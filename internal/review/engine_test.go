package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckEngine(t *testing.T) {
	for name, tc := range map[string]struct {
		pin, version string
		single       bool
		err          string
	}{
		"no atlas.yaml":     {"", "0.13.0", false, ""},
		"same minor":        {`"0.13"`, "v0.13.4", false, ""},
		"other minor":       {`"0.12"`, "0.13.0", false, "the base side pins engine 0.12, this atlas is 0.13.0"},
		"single engine":     {`"0.12"`, "0.13.0", true, ""},
		"development build": {`"0.12"`, "dev", false, ""},
		"invalid file":      {`0.12`, "0.13.0", true, "must be a quoted string"},
	} {
		ws := t.TempDir()
		if tc.pin != "" {
			content := "apiVersion: atlas.io/v1alpha1\nkind: Root\nspec:\n  engine: " + tc.pin + "\n"
			if err := os.WriteFile(filepath.Join(ws, "atlas.yaml"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s := &Session{Workspace: ws, Engine: tc.version, SingleEngine: tc.single}
		err := s.checkEngine(Base)
		if (tc.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: got %v, want %q", name, err, tc.err)
		}
	}
}
