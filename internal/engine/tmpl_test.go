package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tpl context is helmfile's: its `get` (not sprig's), missingkey=error,
// and the atlas helpers of templates/_*.tpl in scope.
func TestTemplater(t *testing.T) {
	tp := NewTemplater()
	out, err := tp.Tpl(`{{ .Values.a | upper }} {{ get "b.c" "none" .Values }} {{ sprigGet .Values "a" }}`, map[string]any{"Values": map[string]any{"a": "hi", "b": map[string]any{"c": 1}}})
	if err != nil || out != "HI 1 hi" {
		t.Errorf("funcs: %q, %v", out, err)
	}
	if _, err := tp.Tpl(`{{ .Values.missing }}`, map[string]any{"Values": map[string]any{}}); err == nil {
		t.Error("missingkey=error: no error")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "x", "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = tp.Tpl(`{{ include "glob" (printf "%s/*/apps" .dir) }}`, map[string]any{"dir": dir})
	if err != nil || !strings.Contains(out, dir+"/x/apps") {
		t.Errorf("helper include: %q, %v", out, err)
	}
}
