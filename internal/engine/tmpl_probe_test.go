package engine

import (
	"testing"

	"github.com/helmfile/helmfile/pkg/filesystem"
	"github.com/helmfile/helmfile/pkg/tmpl"
)

func TestHelmfileTemplateProbe(t *testing.T) {
	c := &tmpl.Context{}
	c.SetBasePath(t.TempDir())
	c.SetFileSystem(filesystem.DefaultFileSystem())
	out, err := c.RenderTemplateToBuffer(`{{ .Values.a | default "x" | upper }} {{ get "b.c" "none" .Values }}`, map[string]any{"Values": map[string]any{"a": "hi", "b": map[string]any{"c": 1}}})
	if err != nil || out.String() != "HI 1" {
		t.Fatalf("%q %v", out, err)
	}
}
