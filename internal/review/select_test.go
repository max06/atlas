package review

import (
	"reflect"
	"testing"

	"github.com/max06/atlas/internal/discovery"
)

func pairMap(keys ...[2]string) *discovery.Map {
	m := &discovery.Map{}
	for _, k := range keys {
		m.Pairs = append(m.Pairs, discovery.Pair{Cluster: k[0], DeploymentName: k[1]})
	}
	return m
}

func TestSelect(t *testing.T) {
	newC := func() *Classification {
		c := &Classification{
			HeadMap: pairMap([2]string{"app/c1", "web"}, [2]string{"app/c1", "db"}, [2]string{"app/c2", "web"}),
			// the deployment "old" exists only on the target side
			BaseMap: pairMap([2]string{"app/c1", "web"}, [2]string{"app/c1", "old"}),
		}
		c.Mode = "full"
		return c
	}
	for _, tc := range []struct {
		name                 string
		clusters, deployment []string
		wantPR, wantBase     []string
	}{
		{"leaf cluster", []string{"c1"}, nil, []string{"app/c1|web", "app/c1|db"}, []string{"app/c1|web", "app/c1|old"}},
		{"deployment on all clusters", nil, []string{"web"}, []string{"app/c1|web", "app/c2|web"}, []string{"app/c1|web"}},
		{"both", []string{"app/c2"}, []string{"web"}, []string{"app/c2|web"}, []string{}},
		{"target side only", nil, []string{"old"}, []string{}, []string{"app/c1|old"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newC()
			if err := c.Select(tc.clusters, tc.deployment); err != nil {
				t.Fatal(err)
			}
			if c.Mode != "subset" {
				t.Errorf("mode %q", c.Mode)
			}
			if !reflect.DeepEqual(*c.SelectedPR, tc.wantPR) || !reflect.DeepEqual(*c.SelectedBaseline, tc.wantBase) {
				t.Errorf("PR %v base %v, want %v %v", *c.SelectedPR, *c.SelectedBaseline, tc.wantPR, tc.wantBase)
			}
		})
	}
	for _, bad := range []struct{ clusters, deployments []string }{
		{[]string{"nope"}, nil},
		{nil, []string{"nope"}},
		{[]string{"c2"}, []string{"db"}}, // both exist, the pair does not
	} {
		if err := newC().Select(bad.clusters, bad.deployments); err == nil {
			t.Errorf("%v %v: must fail", bad.clusters, bad.deployments)
		}
	}
	// a target side without a map (new entry) selects nothing there
	c := newC()
	c.BaseMap = nil
	if err := c.Select(nil, []string{"db"}); err != nil || len(*c.SelectedBaseline) != 0 {
		t.Errorf("nil base map: %v %v", err, c.SelectedBaseline)
	}
}
