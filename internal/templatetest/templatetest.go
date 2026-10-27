// Package templatetest runs test suites of ATLAS templates
// (templates/<name>/tests/*_test.yaml). A test renders the template through
// ATLAS, in the context of a deployment of the tree that instantiates it,
// with the test's values added to that instance, and checks the rendered
// manifests with the assertions of helm-unittest (the same assert syntax and
// documentation).
//
// Proof of concept (atlas-next note 16): suite format, context and output
// are open for review.
package templatetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/helm-unittest/helm-unittest/pkg/unittest"
	"github.com/helm-unittest/helm-unittest/pkg/unittest/results"
	"gopkg.in/yaml.v3"

	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/review"
)

// Suite is one test file.
type Suite struct {
	Name string `yaml:"suite"`
	// Cluster and Deployment name the instance that gives the context
	// (default: the first deployment that uses the template).
	Cluster    string `yaml:"cluster"`
	Deployment string `yaml:"deployment"`
	Tests      []Test `yaml:"tests"`

	file, template string
}

// Test is one case of a suite.
type Test struct {
	It string `yaml:"it"`
	// Values are added to the instance's values (apps[].values) for this test.
	Values []map[string]any `yaml:"values"`
	// Release limits the documents to one release of the deployment.
	Release string                `yaml:"release"`
	Asserts []*unittest.Assertion `yaml:"asserts"`
}

// Options of a run.
type Options struct {
	Dir      string // tree root
	Entry    string // entry helmfile (helmfile engine)
	Helmfile string
	// Templates limits the run to these templates ("" = all with tests).
	Templates []string
	Decrypt   bool
	Work      string // private directory, created and removed
	Log       io.Writer
}

// Result of one test.
type Result struct {
	Suite, File, It string
	Pair            string
	Passed          bool
	Failures        []string
}

// Run discovers the suites and runs them. err is for problems outside the
// tests (a suite that does not parse, no deployment for a template).
func Run(ctx context.Context, o Options) ([]Result, error) {
	if o.Log == nil {
		o.Log = io.Discard
	}
	m, err := review.DiscoverTree(ctx, o.Dir, o.Entry, o.Helmfile)
	if err != nil {
		return nil, err
	}
	suites, err := findSuites(o.Dir, m, o.Templates)
	if err != nil {
		return nil, err
	}
	if len(suites) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(o.Work, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(o.Work)
	tree := filepath.Join(o.Work, "tree")
	if err := copyTree(o.Dir, tree); err != nil {
		return nil, err
	}
	var out []Result
	for _, s := range suites {
		pair, depFile, err := s.context(m, tree)
		if err != nil {
			return out, fmt.Errorf("%s: %w", s.file, err)
		}
		original, err := os.ReadFile(depFile)
		if err != nil {
			return out, err
		}
		type rendered struct {
			docs map[string][]map[string]any
			err  error
		}
		cache := map[string]rendered{} // one render per set of test values
		for _, t := range s.Tests {
			r := Result{Suite: s.Name, File: s.file, It: t.It, Pair: pair}
			key, err := yaml.Marshal(t.Values)
			if err != nil {
				return out, err
			}
			c, ok := cache[string(key)]
			if !ok {
				if err := withValues(depFile, original, s.template, t.Values); err != nil {
					return out, fmt.Errorf("%s: %s: %w", s.file, t.It, err)
				}
				c.docs, c.err = render(ctx, o, tree, pair)
				cache[string(key)] = c
			}
			r.Passed, r.Failures = assert(t, c.docs, c.err)
			out = append(out, r)
		}
		if err := os.WriteFile(depFile, original, 0o644); err != nil {
			return out, err
		}
	}
	return out, nil
}

// findSuites: templates/<name>/tests/*_test.yaml of the known templates.
func findSuites(dir string, m *discovery.Map, only []string) ([]*Suite, error) {
	root := filepath.Join(dir, strings.TrimPrefix(m.TemplatesRoot, "./"))
	known := map[string]bool{}
	for _, p := range m.Pairs {
		for _, t := range p.Templates {
			known[t] = true
		}
	}
	want := map[string]bool{}
	for _, t := range only {
		want[strings.Trim(t, "/")] = true
	}
	var suites []*Suite
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, "_test.yaml") || filepath.Base(filepath.Dir(p)) != "tests" {
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(filepath.Dir(p)))
		template := filepath.ToSlash(rel)
		if len(want) > 0 && !want[template] {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		s := &Suite{}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(s); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		s.file, _ = filepath.Rel(dir, p)
		s.template = template
		if s.Name == "" {
			s.Name = template
		}
		if !known[template] {
			return fmt.Errorf("%s: no deployment uses template %q", s.file, template)
		}
		for _, t := range s.Tests {
			for _, a := range t.Asserts {
				if strings.HasPrefix(a.AssertType, "matchSnapshot") {
					return fmt.Errorf("%s: %q: %s is not supported yet (no snapshot store)", s.file, t.It, a.AssertType)
				}
			}
		}
		suites = append(suites, s)
		return nil
	})
	sort.Slice(suites, func(i, j int) bool { return suites[i].file < suites[j].file })
	return suites, err
}

// context picks the instance: the suite's cluster/deployment, else the
// first pair that uses the template. It returns the pair and the
// deployment.yaml of that instance in the scratch tree.
func (s *Suite) context(m *discovery.Map, tree string) (string, string, error) {
	var pick *discovery.Pair
	pairs := append([]discovery.Pair{}, m.Pairs...)
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key() < pairs[j].Key() })
	for i, p := range pairs {
		uses := false
		for _, t := range p.Templates {
			uses = uses || t == s.template
		}
		if !uses {
			continue
		}
		if s.Cluster != "" && p.Cluster != s.Cluster && !strings.HasSuffix(p.Cluster, "/"+s.Cluster) {
			continue
		}
		if s.Deployment != "" && p.DeploymentName != s.Deployment {
			continue
		}
		pick = &pairs[i]
		break
	}
	if pick == nil {
		return "", "", fmt.Errorf("no deployment of template %q matches cluster %q, deployment %q", s.template, s.Cluster, s.Deployment)
	}
	if pick.DeploymentPath == "" {
		return "", "", fmt.Errorf("the discovery map has no deployment path (map version %d)", m.Version)
	}
	return pick.Key(), filepath.Join(tree, filepath.FromSlash(pick.DeploymentPath)), nil
}

// withValues writes deployment.yaml with the test values appended to the
// values of the template's first instance.
func withValues(file string, original []byte, template string, values []map[string]any) error {
	var doc map[string]any
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	apps, _ := doc["apps"].([]any)
	for _, a := range apps {
		app, ok := a.(map[string]any)
		if !ok || app["template"] != template {
			continue
		}
		list, _ := app["values"].([]any)
		for _, v := range values {
			list = append(list, v)
		}
		if len(list) > 0 {
			app["values"] = list
		}
		data, err := yaml.Marshal(doc)
		if err != nil {
			return err
		}
		return os.WriteFile(file, data, 0o644)
	}
	return fmt.Errorf("%s has no instance of template %q", file, template)
}

// render renders the pair and returns its documents by file
// ("<release>/<path in the release>").
func render(ctx context.Context, o Options, tree, pair string) (map[string][]map[string]any, error) {
	out := filepath.Join(o.Work, "out")
	err := review.RenderTree(ctx, tree, o.Entry, o.Helmfile, []string{pair}, out, review.RenderOptions{SkipSecrets: !o.Decrypt}, o.Log)
	cluster, dep, _ := strings.Cut(pair, "|")
	base := filepath.Join(out, filepath.FromSlash(cluster), dep)
	docs := map[string][]map[string]any{}
	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var m map[string]any
			if err := dec.Decode(&m); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if m != nil {
				docs[rel] = append(docs[rel], m)
			}
		}
		return nil
	})
	if err == nil {
		err = walkErr
	}
	return docs, err
}

// assert runs the test's assertions. An assert's template selects files:
// the path in the release ("<release>/<chart>/templates/x.yaml"), the path
// in the chart ("templates/x.yaml") or a glob of either; none: every file.
func assert(t Test, docs map[string][]map[string]any, renderErr error) (bool, []string) {
	passed := true
	var failures []string
	for i, a := range t.Asserts {
		var selected []map[string]any
		files := make([]string, 0, len(docs))
		for f := range docs {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			if t.Release != "" && !strings.HasPrefix(f, t.Release+"/") {
				continue
			}
			if a.Template == "" || matches(a.Template, f) {
				for _, d := range docs[f] {
					selected = append(selected, d)
				}
			}
		}
		cfg := unittest.AssertionConfigBuilder{
			// one post-rendered "manifest.yaml": the documents this assert
			// selected (helm-unittest's own template selection is internal)
			TemplatesResult: map[string][]map[string]any{"manifest.yaml": selected},
			RenderSucceed:   renderErr == nil,
			RenderError:     renderErr,
			DidPostRender:   true,
		}.Build()
		template := a.Template
		a.WithConfig(cfg)
		res := a.Assert(&results.AssertionResult{Index: i})
		a.Template = template // Assert rewrites it to the single result key
		if !res.Passed {
			passed = false
			failures = append(failures, fmt.Sprintf("- asserts[%d] %s fail", i, res.AssertType))
			shown := template
			if shown == "" {
				shown = "(all files)"
			}
			if t.Release != "" {
				shown += " in release " + t.Release
			}
			for _, l := range res.FailInfo {
				// the single post-rendered key stands for the selection
				failures = append(failures, "    "+strings.Replace(l, "Template:\tmanifest.yaml", "Template:\t"+shown, 1))
			}
		}
	}
	return passed, failures
}

func matches(pattern, file string) bool {
	inChart := file
	if parts := strings.SplitN(file, "/", 3); len(parts) == 3 {
		inChart = parts[2] // <release>/<chart>/<path>
	}
	for _, f := range []string{file, inChart} {
		if f == pattern {
			return true
		}
		if ok, _ := path.Match(pattern, f); ok {
			return true
		}
	}
	return false
}

// copyTree copies dir without its .git into dst.
func copyTree(dir, dst string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
