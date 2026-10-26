package engine

import (
	"strings"
	"testing"
)

func TestTemplateEq(t *testing.T) {
	for _, tc := range []struct {
		a, b   any
		eq, ok bool
	}{
		{"x", "x", true, true},
		{"x", "y", false, true},
		{1, int64(1), true, true},
		{1.5, 1.5, true, true},
		{nil, nil, true, true},
		{"1", 1, false, false}, // incompatible kinds: the template's eq fails
	} {
		eq, err := tEq(tc.a, tc.b)
		if eq != tc.eq || (err == nil) != tc.ok {
			t.Errorf("eq %#v %#v: %v, %v", tc.a, tc.b, eq, err)
		}
	}
}

func TestMungedAndRange(t *testing.T) {
	if got := munged("vm", "cust", true, "suffix"); got != "vm-cust" {
		t.Errorf("suffix: %s", got)
	}
	if got := munged("vm", "backend", true, "prefix"); got != "backend-vm" {
		t.Errorf("prefix: %s", got)
	}
	if got := munged("vm", "vm", false, nil); got != "vm" {
		t.Errorf("no munge: %s", got)
	}
	// range over a map iterates its values in key order
	got, err := rangeValues(map[string]any{"b": 2, "a": 1}, "x")
	if err != nil || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("map range: %v, %v", got, err)
	}
	if _, err := rangeValues("text", "x"); err == nil {
		t.Error("range over a string: no error")
	}
}

func TestReleaseContext(t *testing.T) {
	rel := map[string]any{"name": "app", "namespace": "team", "chart": "/c", "labels": map[string]any{"tier": 1}}
	got := ReleaseContext(rel, "")
	if got["Name"] != "app" || got["Namespace"] != "team" || got["Chart"] != "/c" || got["Labels"].(map[string]any)["tier"] != "1" {
		t.Errorf("context: %v", got)
	}
	// helmfile's --namespace wins, as in the template engine's release time
	if got := ReleaseContext(rel, "argo"); got["Namespace"] != "argo" {
		t.Errorf("override: %v", got)
	}
	if got := ReleaseContext(map[string]any{"name": "x"}, ""); got["Namespace"] != "" {
		t.Errorf("no namespace: %v", got)
	}
}

func TestFieldMissingKey(t *testing.T) {
	if _, err := field(map[string]any{}, "apps", "d"); err == nil || !strings.Contains(err.Error(), "no entry for key") {
		t.Errorf("missing key: %v", err)
	}
	if _, err := field(nil, "apps", "d"); err == nil {
		t.Error("nil map: no error")
	}
}
