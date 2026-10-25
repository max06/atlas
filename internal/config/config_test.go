package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const header = "apiVersion: atlas.io/v1alpha1\nkind: Root\n"

func TestParse(t *testing.T) {
	r, err := Parse([]byte(header + "spec:\n  engine: \"0.13\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Spec.Engine.String() != "0.13" || r.Spec.Engine.Plugin() != "atlas-v0-13" {
		t.Errorf("engine %s plugin %s", r.Spec.Engine, r.Spec.Engine.Plugin())
	}
	if r.Spec.TemplateDir != DefaultTemplateDir || r.Spec.DeploymentDir != DefaultDeploymentDir {
		t.Errorf("defaults: %q %q", r.Spec.TemplateDir, r.Spec.DeploymentDir)
	}

	r, err = Parse([]byte(header + "metadata:\n  name: x\nspec:\n  engine: '1.10'\n  templateDir: ./lib/templates/\n  deploymentDir: clusters\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Spec.Engine.Major != 1 || r.Spec.Engine.Minor != 10 || r.Spec.TemplateDir != "lib/templates" || r.Spec.DeploymentDir != "clusters" {
		t.Errorf("got %+v", r.Spec)
	}

	// "0.0" is a value, not a missing key.
	if _, err := Parse([]byte(header + "spec:\n  engine: \"0.0\"\n")); err != nil {
		t.Errorf("0.0: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"unquoted float":   {header + "spec:\n  engine: 1.10\n", `must be a quoted string ("1.10", not 1.10), found !!float 1.10`},
		"unquoted int":     {header + "spec:\n  engine: 1\n", "found !!int 1"},
		"patch version":    {header + "spec:\n  engine: \"0.13.1\"\n", "is not <major>.<minor>"},
		"leading v":        {header + "spec:\n  engine: \"v0.13\"\n", "is not <major>.<minor>"},
		"missing engine":   {header + "spec:\n  templateDir: t\n", "spec.engine is required"},
		"no spec":          {header, "spec.engine is required"},
		"unknown key":      {header + "spec:\n  engine: \"0.13\"\n  templateDirs: t\n", "field templateDirs not found"},
		"top-level key":    {header + "engine: \"0.13\"\n", "field engine not found"},
		"wrong apiVersion": {"apiVersion: atlas.p8e.io/v1\nkind: Root\nspec:\n  engine: \"0.13\"\n", "this atlas reads atlas.io/v1alpha1/Root"},
		"wrong kind":       {"apiVersion: atlas.io/v1alpha1\nkind: Deployment\nspec:\n  engine: \"0.13\"\n", `"Deployment"`},
		"escaping dir":     {header + "spec:\n  engine: \"0.13\"\n  templateDir: ../shared\n", "must be a relative path inside the tree"},
		"absolute dir":     {header + "spec:\n  engine: \"0.13\"\n  deploymentDir: /srv/d\n", "must be a relative path inside the tree"},
		"two documents":    {header + "spec:\n  engine: \"0.13\"\n---\nfoo: bar\n", "exactly one YAML document"},
		"empty":            {"", "the file is empty"},
		"broken yaml":      {header + "spec:\n  engine: \"0.13\n", "yaml:"},
	} {
		_, err := Parse([]byte(tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(header+"spec:\n  engine: 1.10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.HasPrefix(err.Error(), "atlas.yaml: ") {
		t.Fatalf("invalid file: %v", err)
	}
}

func TestEngineOfVersion(t *testing.T) {
	for in, want := range map[string]string{"v0.13.2": "0.13", "1.0.0": "1.0", "dev": "", "sha-33294c5": "", "v0.13": "", "main": ""} {
		e, ok := EngineOfVersion(in)
		if got := map[bool]string{true: e.String(), false: ""}[ok]; got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}

func TestEngineMarshalsQuoted(t *testing.T) {
	e, _ := ParseEngine("1.10")
	out, err := yaml.Marshal(map[string]Engine{"engine": e})
	if err != nil || string(out) != "engine: \"1.10\"\n" {
		t.Errorf("got %q, %v", out, err)
	}
}
