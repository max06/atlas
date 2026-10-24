// Package tree runs the render tree of a recursive ATLAS repository the way
// Argo CD builds it.
//
// A parent deployment renders Argo CD Applications that call the atlas plugin
// again. Argo CD renders each such Application; this package does the same for
// the CLI: it finds the calls in rendered output, renders each call with its
// plugin environment, and repeats down to the leaves.
//
// The package knows only the call convention: an Application whose plugin
// environment holds ATLAS_DEPLOYMENT is a child render with the context
// ATLAS_FILTER_CLUSTER, ATLAS_FILTER_DEPLOYMENT_NAME, ATLAS_DEPLOYMENT,
// ATLAS_DEPLOYMENT_BASE and ATLAS_PARENTS. What a cluster, a group or a parent
// template is stays in the ATLAS templates (the content side).
//
// An Application without ATLAS_DEPLOYMENT renders a deployment from its
// deployment.yaml. That render is part of the flat (discovered) render
// already, so the tree does not render it again.
package tree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxDepth limits the nesting of child renders.
const MaxDepth = 10

// Call is one child render, taken from an Application's plugin environment.
type Call struct {
	App        string   // namespace/name of the Application that makes the call
	Cluster    string   // ATLAS_FILTER_CLUSTER
	Deployment string   // ATLAS_FILTER_DEPLOYMENT_NAME
	Definition string   // ATLAS_DEPLOYMENT
	Base       string   // ATLAS_DEPLOYMENT_BASE
	Parents    []string // ATLAS_PARENTS, outer first
}

// Key identifies a call by its context: the same cluster, deployment,
// definition and base give the same render.
func (c Call) Key() string {
	sum := sha256.Sum256([]byte(c.Definition))
	return c.Cluster + "\x00" + c.Deployment + "\x00" + c.Base + "\x00" + hex.EncodeToString(sum[:8])
}

// App is one rendered Argo CD Application.
type App struct {
	Ref     string // namespace/name
	Call    *Call  // nil when the Application renders a deployment.yaml
	Cluster string // ATLAS_FILTER_CLUSTER of an on-disk Application ("" if absent)
	Name    string // ATLAS_FILTER_DEPLOYMENT_NAME of an on-disk Application
}

type pluginEnv struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type application struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Source *struct {
			Plugin *struct {
				Env []pluginEnv `yaml:"env"`
			} `yaml:"plugin"`
		} `yaml:"source"`
	} `yaml:"spec"`
}

// Apps parses a multi-document manifest stream and returns every Argo CD
// Application in it. Documents that are not Applications are skipped.
func Apps(manifests []byte) ([]App, error) {
	var apps []App
	dec := yaml.NewDecoder(bytes.NewReader(manifests))
	for {
		var doc application
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse rendered manifests: %w", err)
		}
		if doc.Kind != "Application" || !strings.HasPrefix(doc.APIVersion, "argoproj.io/") {
			continue
		}
		ns := doc.Metadata.Namespace
		if ns == "" {
			ns = "argocd"
		}
		app := App{Ref: ns + "/" + doc.Metadata.Name}
		env := map[string]string{}
		if doc.Spec.Source != nil && doc.Spec.Source.Plugin != nil {
			for _, e := range doc.Spec.Source.Plugin.Env {
				env[e.Name] = e.Value
			}
		}
		if def, ok := env["ATLAS_DEPLOYMENT"]; ok && def != "" {
			call := &Call{
				App:        app.Ref,
				Cluster:    env["ATLAS_FILTER_CLUSTER"],
				Deployment: env["ATLAS_FILTER_DEPLOYMENT_NAME"],
				Definition: def,
				Base:       env["ATLAS_DEPLOYMENT_BASE"],
			}
			for _, p := range strings.Split(env["ATLAS_PARENTS"], ",") {
				if p = strings.TrimSpace(p); p != "" {
					call.Parents = append(call.Parents, p)
				}
			}
			if call.Cluster == "" || call.Deployment == "" || call.Base == "" {
				return nil, fmt.Errorf("application %s calls atlas with ATLAS_DEPLOYMENT but without ATLAS_FILTER_CLUSTER, ATLAS_FILTER_DEPLOYMENT_NAME or ATLAS_DEPLOYMENT_BASE", app.Ref)
			}
			app.Call = call
		} else {
			app.Cluster = env["ATLAS_FILTER_CLUSTER"]
			app.Name = env["ATLAS_FILTER_DEPLOYMENT_NAME"]
		}
		apps = append(apps, app)
	}
	return apps, nil
}

// RenderFunc renders one call and returns its manifests.
type RenderFunc func(Call) ([]byte, error)

// Result is the outcome of a tree run.
type Result struct {
	Calls []Call // child renders in the order they ran
}

// Walk renders the children found in flat (the render of the discovered
// deployments) and in every child render, down to the leaves.
//
// onDisk lists the discovered pairs as "cluster/deployment" (any order). Walk
// fails when:
//   - two Applications have the same namespace/name (two parents would own
//     one Application in Argo CD),
//   - a child render has the cluster and name of a discovered deployment
//     (its render would replace the deployment's render),
//   - a child render calls itself again with the same context (cycle), or
//     the nesting exceeds MaxDepth child renders.
func Walk(flat []byte, onDisk []string, render RenderFunc) (Result, error) {
	var res Result
	pairs := map[string]bool{}
	for _, p := range onDisk {
		pairs[p] = true
	}
	owners := map[string]string{} // Application ref -> who rendered it
	done := map[string]bool{}     // call keys rendered
	type item struct {
		call     Call
		ancestry []string // keys of the child renders above this call, outer first
	}
	var queue []item

	collect := func(manifests []byte, from string, ancestry []string) error {
		apps, err := Apps(manifests)
		if err != nil {
			return err
		}
		for _, app := range apps {
			if prev, ok := owners[app.Ref]; ok {
				return fmt.Errorf("render tree: application %s comes from two places (%s and %s) — Argo CD would give it two parents; rename one", app.Ref, prev, from)
			}
			owners[app.Ref] = from
			if app.Call != nil {
				queue = append(queue, item{call: *app.Call, ancestry: ancestry})
			}
		}
		return nil
	}

	if err := collect(flat, "the flat render", nil); err != nil {
		return res, err
	}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		call, key := next.call, next.call.Key()
		pair := call.Cluster + "/" + call.Deployment
		for _, k := range next.ancestry {
			if k == key {
				return res, fmt.Errorf("render tree: cycle — child %s (application %s) renders itself again: %s", pair, call.App, strings.Join(append(append([]string{}, call.Parents...), call.Deployment), " -> "))
			}
		}
		if done[key] {
			continue
		}
		if pairs[pair] {
			return res, fmt.Errorf("render tree: child %s (application %s) has the cluster and name of a deployment on disk — rename the child", pair, call.App)
		}
		if len(next.ancestry) >= MaxDepth {
			return res, fmt.Errorf("render tree: %s is nested more than %d levels deep: %s", pair, MaxDepth, strings.Join(append(append([]string{}, call.Parents...), call.Deployment), " -> "))
		}
		done[key] = true
		out, err := render(call)
		if err != nil {
			return res, fmt.Errorf("render child %s (application %s): %w", pair, call.App, err)
		}
		res.Calls = append(res.Calls, call)
		if err := collect(out, "child "+pair, append(append([]string{}, next.ancestry...), key)); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Summary lists the calls of a result, one line each, sorted.
func (r Result) Summary() []string {
	var lines []string
	for _, c := range r.Calls {
		chain := append(append([]string{}, c.Parents...), c.Deployment)
		lines = append(lines, fmt.Sprintf("%s/%s (via %s)", c.Cluster, c.Deployment, strings.Join(chain, " -> ")))
	}
	sort.Strings(lines)
	return lines
}

// ReadManifests concatenates every *.yaml / *.yml file below dir into one
// multi-document stream, in a stable order. A missing dir is an empty stream.
func ReadManifests(dir string) ([]byte, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			files = append(files, p)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out bytes.Buffer
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		out.WriteString("---\n")
		out.Write(b)
		out.WriteString("\n")
	}
	return out.Bytes(), nil
}
