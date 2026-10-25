package ci

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/max06/atlas/internal/gha"
)

// The reference check reads the consumer's workflow files: a `uses:` key
// counts, the same text in a comment does not.
func TestWorkflowPinIgnoresComments(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# The review is the atlas action (`uses: max06/atlas@<release>`).\n" +
		"name: ATLAS Review\n" +
		"jobs:\n  review:\n    steps:\n      - uses: max06/atlas@v0.11.0\n        with:\n          render-subset: \"on\"\n"
	if err := os.WriteFile(filepath.Join(wf, "atlas-review.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WorkflowPin(dir); got != "v0.11.0" {
		t.Fatalf("WorkflowPin = %q, want v0.11.0", got)
	}
	// the deprecated reusable workflow and a quoted reference
	content = "jobs:\n  review:\n    uses: 'max06/atlas/.github/workflows/snapshot-review.yml@v0.9.2'\n"
	if err := os.WriteFile(filepath.Join(wf, "atlas-review.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WorkflowPin(dir); got != "v0.9.2" {
		t.Fatalf("WorkflowPin = %q, want v0.9.2", got)
	}
	// nothing but a comment
	if err := os.WriteFile(filepath.Join(wf, "atlas-review.yml"), []byte("# uses: max06/atlas@main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WorkflowPin(dir); got != "" {
		t.Fatalf("WorkflowPin = %q, want empty", got)
	}
}

// The key file reaches helmfile through the render's own environment, also
// when the caller (the review) wrote the file and the process environment
// has no SOPS_AGE_KEY_FILE — the CI case.
func TestSopsKeyEnv(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY_FILE", "")
	tmp := t.TempDir()
	log := gha.Log{W: io.Discard}

	env, err := sopsKeyEnv(RenderConfig{Temp: tmp, SopsAgeKeyFile: "/keys/review.txt"}, log)
	if err != nil || len(env) != 1 || env[0] != "SOPS_AGE_KEY_FILE=/keys/review.txt" {
		t.Errorf("given key file: %v, %v", env, err)
	}
	env, err = sopsKeyEnv(RenderConfig{Temp: tmp, SopsAgeKey: "AGE-SECRET-KEY-1TEST"}, log)
	want := "SOPS_AGE_KEY_FILE=" + filepath.Join(tmp, "sops-age-key.txt")
	if err != nil || len(env) != 1 || env[0] != want {
		t.Errorf("key from input: %v, %v", env, err)
	}
	if data, _ := os.ReadFile(filepath.Join(tmp, "sops-age-key.txt")); string(data) != "AGE-SECRET-KEY-1TEST" {
		t.Errorf("key file content %q", data)
	}
	if env, _ := sopsKeyEnv(RenderConfig{Temp: tmp, SopsAgeKeyFile: "/keys/review.txt", Keyless: true}, log); env != nil {
		t.Errorf("keyless: %v", env)
	}
	if env, _ := sopsKeyEnv(RenderConfig{Temp: tmp}, log); env != nil {
		t.Errorf("no key: %v", env)
	}
}
