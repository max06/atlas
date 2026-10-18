package gha

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputsDelimiter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	o := Outputs{Path: path}
	if err := o.SetRaw("body", "line\n"); err != nil {
		t.Fatal(err)
	}
	// a value with an ATLAS_EOF line (a render log in the comment) must not
	// end the output early and inject other outputs
	if err := o.SetRaw("body2", "log:\nATLAS_EOF\npr_status=success\n"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetRaw("body3", "no trailing newline"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(string(data), "\n")
	if lines[0] != "body<<ATLAS_EOF" || lines[2] != "ATLAS_EOF" {
		t.Fatalf("plain value keeps ATLAS_EOF:\n%s", data)
	}
	open := lines[3]
	if !strings.HasPrefix(open, "body2<<ATLAS_EOF_") {
		t.Fatalf("injected value needs a random delimiter: %q", open)
	}
	d := strings.TrimPrefix(open, "body2<<")
	if lines[7] != d {
		t.Fatalf("delimiter %q must close after the value:\n%s", d, data)
	}
	if lines[8] != "body3<<ATLAS_EOF" || lines[9] != "no trailing newline" || lines[10] != "ATLAS_EOF" {
		t.Fatalf("missing final newline:\n%s", data)
	}
}
