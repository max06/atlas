package ci

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/max06/atlas/internal/tree"
)

// children renders the child Applications (recursive ATLAS) that the side's
// render contains, down to the leaves (internal/tree), into the same
// snapshot tree. Each child becomes a pair "<cluster>|<deployment>" of the
// side, so the redaction backfill, the scrub and the diff treat it like a
// discovered deployment. In RedactInCLI mode every child gets its own map
// build first: the map build of the side covers the discovered pairs only.
//
// Only children of rendered parents are found: in a subset render a child
// renders when its parent is selected.
//
// It returns the number of child renders; a tree error (two parents for one
// Application, a child with the name of a deployment on disk, a cycle) or a
// failed child render is a render error of the side.
func (s *renderSide) children() (int, error) {
	flat, err := tree.ReadManifests(s.snapshot)
	if err != nil {
		return 0, err
	}
	onDisk := make([]string, 0, len(s.pairs))
	for _, p := range s.pairs {
		onDisk = append(onDisk, strings.Replace(p, "|", "/", 1))
	}
	render := func(c tree.Call) ([]byte, error) {
		opts := s.renderOptions([]string{c.Cluster}, []string{c.Deployment})
		opts.Definition, opts.DefinitionBase, opts.Parents = c.Definition, c.Base, c.Parents
		if s.RedactInCLI {
			mapOpts := opts
			mapOpts.RedactMaps = true
			var stream bytes.Buffer
			if err := s.run(s.Timeout, mapOpts, &stream, "build", "--allow-no-matching-release"); err != nil {
				s.log.Annotate(s.level, "Redaction maps failed for child %s/%s on %s", c.Cluster, c.Deployment, s.sideName)
				return nil, err
			}
			if _, err := WriteRedactMaps(stream.Bytes(), s.SidedumpDir); err != nil {
				return nil, err
			}
		}
		sel := []string{"--selector", "cluster=" + c.Cluster + ",deploymentName=" + c.Deployment}
		if err := s.run(s.Timeout, opts, nil, s.templateArgs(sel)...); err != nil {
			s.log.Annotate(s.level, "Render failed for child %s/%s (application %s) on %s", c.Cluster, c.Deployment, c.App, s.sideName)
			return nil, err
		}
		s.pairs = append(s.pairs, c.Cluster+"|"+c.Deployment)
		return tree.ReadManifests(filepath.Join(s.snapshot, filepath.FromSlash(c.Cluster), c.Deployment))
	}
	res, err := tree.Walk(flat, onDisk, render)
	if err != nil {
		s.log.Annotate(s.level, "Render tree on %s: %v", s.sideName, err)
		return len(res.Calls), err
	}
	if len(res.Calls) > 0 {
		s.log.Printf("child renders on %s: %d", s.sideName, len(res.Calls))
		for _, line := range res.Summary() {
			s.log.Printf("  %s", line)
		}
	}
	return len(res.Calls), nil
}
