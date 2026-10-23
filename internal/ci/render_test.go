package ci

import (
	"os"
	"path/filepath"
	"testing"
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
