package cli

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/max06/atlas/internal/config"
)

// pluginsDir creates a plugin socket directory like the repo-server's.
func pluginsDir(t *testing.T, plugins ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range plugins {
		l, err := net.Listen("unix", filepath.Join(dir, p+".sock"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
	}
	// a plain file with the suffix is not a plugin
	if err := os.WriteFile(filepath.Join(dir, "stale.sock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func tree(t *testing.T, atlasYAML string) string {
	t.Helper()
	dir := t.TempDir()
	if atlasYAML != "" {
		if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(atlasYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func pinned(engine string) string {
	return "apiVersion: atlas.io/v1alpha1\nkind: Root\nspec:\n  engine: " + engine + "\n"
}

func TestCmpMatch(t *testing.T) {
	sockets := pluginsDir(t, "atlas-v0-13", "atlas-v1-0", "helmfile")
	v013, _ := config.ParseEngine("0.13")
	v10, _ := config.ParseEngine("1.0")
	sidecars := map[string]matcher{
		"atlas-v0-13": {self: v013, pluginsDir: sockets},
		"atlas-v1-0":  {self: v10, pluginsDir: sockets},
		"helmfile":    {legacy: true, pluginsDir: sockets},
	}
	for name, tc := range map[string]struct {
		atlasYAML string
		want      string // the one sidecar that matches; "" = all fail with err
		err       string
	}{
		"no atlas.yaml":  {"", "helmfile", ""},
		"v0 pin":         {pinned(`"0.13"`), "atlas-v0-13", ""},
		"v1 pin":         {pinned(`"1.0"`), "atlas-v1-0", ""},
		"unknown pin":    {pinned(`"9.9"`), "", `pins engine "9.9", but no Argo CD plugin atlas-v9-9 is installed (installed: atlas-v0-13, atlas-v1-0, helmfile)`},
		"unquoted float": {pinned(`1.10`), "", "must be a quoted string"},
		"broken":         {pinned(`"1.0`), "", "atlas.yaml: yaml:"},
	} {
		var errs []string
		for sidecar, m := range sidecars {
			line, err := m.match(tree(t, tc.atlasYAML))
			switch {
			case err != nil:
				errs = append(errs, err.Error())
			case line != "" && sidecar != tc.want:
				t.Errorf("%s: %s matched (%q), want %q", name, sidecar, line, tc.want)
			case line == "" && sidecar == tc.want:
				t.Errorf("%s: %s did not match", name, sidecar)
			}
		}
		if tc.err == "" {
			if len(errs) > 0 {
				t.Errorf("%s: errors %v", name, errs)
			}
			continue
		}
		// Argo CD shows the error of the last sidecar it asked: all must agree.
		if len(errs) != len(sidecars) {
			t.Errorf("%s: %d of %d sidecars failed: %v", name, len(errs), len(sidecars), errs)
		}
		for _, e := range errs {
			if e != errs[0] || !strings.Contains(e, tc.err) {
				t.Errorf("%s: got %q, want all equal and containing %q", name, e, tc.err)
			}
		}
	}
}

func TestCmpMatchOutsideRepoServer(t *testing.T) {
	v10, _ := config.ParseEngine("1.0")
	m := matcher{self: v10, pluginsDir: filepath.Join(t.TempDir(), "missing")}
	if line, err := m.match(tree(t, pinned(`"1.0"`))); err != nil || line == "" {
		t.Errorf("got %q, %v", line, err)
	}
	if line, err := m.match(tree(t, pinned(`"9.9"`))); err != nil || line != "" {
		t.Errorf("unknown pin without sockets: got %q, %v", line, err)
	}
}

func TestCmpCommands(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	run := func(args ...string) (string, error) {
		root := NewRoot()
		var out strings.Builder
		root.SetOut(&out)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}
	dir := tree(t, pinned(`"0.13"`))

	Version = "v0.13.2"
	out, err := run("cmp", "match", "-C", dir, "--plugins-dir", filepath.Join(t.TempDir(), "missing"))
	if err != nil || out != "atlas.yaml pins engine 0.13: plugin atlas-v0-13\n" {
		t.Errorf("release build match: %q, %v", out, err)
	}
	out, err = run("cmp", "config")
	if err != nil || !strings.Contains(out, "version: v0-13\n") || !strings.Contains(out, `command: [atlas, cmp, match, --engine, "0.13"]`) {
		t.Errorf("release build config: %q, %v", out, err)
	}

	Version = "dev"
	if _, err := run("cmp", "match", "-C", dir); err == nil || !strings.Contains(err.Error(), "pass --engine") {
		t.Errorf("dev build without --engine: %v", err)
	}
	out, err = run("cmp", "config")
	if err != nil || !strings.Contains(out, "version: dev\n") || strings.Contains(out, "discover") {
		t.Errorf("dev build config: %q, %v", out, err)
	}
	out, err = run("cmp", "config", "--engine", "1.0")
	if err != nil || !strings.Contains(out, "version: v1-0\n") {
		t.Errorf("dev build config --engine: %q, %v", out, err)
	}
	if _, err := run("cmp", "match", "--legacy", "--engine", "1.0", "-C", dir); err == nil {
		t.Error("--legacy with --engine: no error")
	}
	out, err = run("cmp", "match", "--legacy", "-C", t.TempDir(), "--plugins-dir", t.TempDir())
	if err != nil || out != "no atlas.yaml: legacy plugin\n" {
		t.Errorf("legacy match: %q, %v", out, err)
	}
}
