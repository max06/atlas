package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorExcerptKeepsErrorsDropsProgress(t *testing.T) {
	log := filepath.Join(t.TempDir(), "pr-stderr.log")
	content := strings.Repeat("wrote /tmp/snapshot/a/b/c.yaml\n", 60) +
		"Templating release=x, chart=y\n" +
		"in ./helmfile.yaml.gotmpl: in .helmfiles[0]: unexpected error: fork/exec /usr/local/bin/helm: argument list too long\n" +
		"in ./helmfile.yaml.gotmpl: in .helmfiles[0]: unexpected error: fork/exec /usr/local/bin/helm: argument list too long\n" +
		"Error: values-loader: release \"db\": values file not found: /x/values.yaml\n" +
		strings.Repeat("wrote /tmp/snapshot/d/e/f.yaml\n", 10)
	if err := os.WriteFile(log, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := errorExcerpt(log, 50)
	if strings.Contains(got, "wrote ") || strings.Contains(got, "Templating release") {
		t.Fatalf("progress lines in the excerpt:\n%s", got)
	}
	if !strings.Contains(got, "argument list too long  (×2)") {
		t.Fatalf("duplicate error not folded:\n%s", got)
	}
	if !strings.Contains(got, "values file not found") {
		t.Fatalf("error line missing:\n%s", got)
	}
	if lines := strings.Count(got, "\n") + 1; lines != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", lines, got)
	}
}

func TestErrorExcerptFallsBackToTail(t *testing.T) {
	log := filepath.Join(t.TempDir(), "pr-stderr.log")
	if err := os.WriteFile(log, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := errorExcerpt(log, 2); got != "two\nthree" {
		t.Fatalf("tail fallback: %q", got)
	}
	if got := errorExcerpt(filepath.Join(t.TempDir(), "missing"), 2); got != "" {
		t.Fatalf("missing file: %q", got)
	}
}
