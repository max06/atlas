package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/helmfile"
	"github.com/max06/atlas/internal/tree"
)

// renderTree runs the flat render (args already hold the filter selectors
// and the output dir), then the child renders it calls (internal/tree).
func renderTree(ctx context.Context, r helmfile.Runner, sel selection, args, extra []string, outputDir string) error {
	flat, err := renderOnce(ctx, r, sel.options(false), args, outputDir, "")
	if err != nil {
		return sel.explain(ctx, r, err)
	}
	apps, err := tree.Apps(flat)
	if err != nil {
		return err
	}
	hasCalls := false
	for _, a := range apps {
		hasCalls = hasCalls || a.Call != nil
	}
	if !hasCalls {
		fmt.Fprintln(os.Stderr, "atlas: tree: no child renders")
		return nil
	}
	qr, log := quiet(r)
	m, err := discovery.Discover(ctx, qr)
	if err != nil {
		return withLog(fmt.Errorf("render tree: discovery: %w", err), log)
	}
	if !m.Has(discovery.CapabilityChildRender) {
		return fmt.Errorf("the ATLAS version this repository pins (%s) cannot render children (no childRender capability) — update the pin", r.File)
	}
	var onDisk []string
	for _, p := range m.Pairs {
		onDisk = append(onDisk, p.Cluster+"/"+p.DeploymentName)
	}
	render := func(c tree.Call) ([]byte, error) {
		opts := sel.options(false)
		opts.Clusters, opts.Deployments = []string{c.Cluster}, []string{c.Deployment}
		opts.Definition, opts.DefinitionBase, opts.Parents = c.Definition, c.Base, c.Parents
		childArgs := append(helmfile.TemplateArgs(), "--selector", "cluster="+c.Cluster+",deploymentName="+c.Deployment)
		if outputDir != "" {
			childArgs = append(childArgs, "--output-dir", outputDir, "--output-dir-template", helmfile.OutputDirTemplate)
		}
		return renderOnce(ctx, r, opts, append(childArgs, extra...), outputDir, filepath.Join(c.Cluster, c.Deployment))
	}
	res, err := tree.Walk(flat, onDisk, render)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "atlas: tree: %d child render(s)\n", len(res.Calls))
	for _, line := range res.Summary() {
		fmt.Fprintln(os.Stderr, "  "+line)
	}
	return nil
}

// renderOnce runs one helmfile template and returns the manifests it made.
// Without an output dir the manifests stream to stdout as well. With one,
// helmfile writes files; the manifests are read back from <dir>/<sub>
// (sub "" = the whole dir).
func renderOnce(ctx context.Context, r helmfile.Runner, opts helmfile.Options, args []string, outputDir, sub string) ([]byte, error) {
	if outputDir == "" {
		var buf bytes.Buffer
		if err := r.Run(ctx, opts, io.MultiWriter(os.Stdout, &buf), args...); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	if err := r.Run(ctx, opts, os.Stdout, args...); err != nil {
		return nil, err
	}
	return readManifests(filepath.Join(outputDir, sub))
}

// readManifests concatenates every *.yaml / *.yml file below dir as one
// multi-document stream, in a stable order.
func readManifests(dir string) ([]byte, error) {
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
