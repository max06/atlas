// Package toolchain checks the external tools an ATLAS render depends on.
//
// Each CLI release records the helm/helmfile versions its test matrix covers
// (Tested*). A mismatch is a warning, not an error: newer patch releases are
// usually fine, but the review and the Argo CD plugin should not drift apart
// silently — that is what the four hand-kept version pins could not prevent.
package toolchain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Versions this release is tested with, and the floors ATLAS enforces.
const (
	TestedHelm     = "4.2.3"
	TestedHelmfile = "1.7.3"
	MinHelm        = "4.0.0"
	MinHelmfile    = "1.0.0"
	// git merge-tree --write-tree (local merge result for reviews)
	MinGit = "2.38.0"
)

// Tool is one checked binary.
type Tool struct {
	Name, Found, Tested, Min string
	Err                      error
}

// Status is "ok", "untested" (works, differs from Tested), "too old" or "missing".
func (t Tool) Status() string {
	switch {
	case t.Err != nil:
		return "missing"
	case less(t.Found, t.Min):
		return "too old"
	case t.Tested != "" && t.Found != t.Tested:
		return "untested"
	}
	return "ok"
}

var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

func version(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "HELMFILE_UPGRADE_NOTICE_DISABLED=1")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	v := semverRe.FindString(string(out))
	if v == "" {
		return "", fmt.Errorf("no version in %q", strings.TrimSpace(string(out)))
	}
	return v, nil
}

// Check probes helm, helmfile and git.
func Check(ctx context.Context) []Tool {
	tools := []Tool{
		{Name: "helm", Tested: TestedHelm, Min: MinHelm},
		{Name: "helmfile", Tested: TestedHelmfile, Min: MinHelmfile},
		{Name: "git", Min: MinGit},
	}
	args := map[string][]string{
		"helm":     {"version", "--short"},
		"helmfile": {"version"},
		"git":      {"version"},
	}
	for i := range tools {
		tools[i].Found, tools[i].Err = version(ctx, tools[i].Name, args[tools[i].Name]...)
	}
	return tools
}

func less(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}
