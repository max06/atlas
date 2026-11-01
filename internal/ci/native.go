package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/max06/atlas/internal/engine"
	"github.com/max06/atlas/internal/native"
)

// nativeLayout: native.Layout (atlas.yaml spec.native).
var nativeLayout = native.Layout

// discoverNative is the discover step of a native tree: the engine's map,
// without helmfile.
func discoverNative(l engine.Layout, out string, log io.Writer) error {
	m, err := engine.DiscoveryMap(l, engine.Filter{}, engine.Switches{})
	if err != nil {
		return &ExitError{1, "discover (native engine): " + err.Error()}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(log, "discover (native engine): %d pairs → %s\n", len(m.Pairs), out)
	return nil
}

// renderNative renders the side with the native engine (internal/native):
// the pairs of the pairs file (subset) or of the discovery map (full). Like
// the helmfile path, a failed pair or release does not stop the others: the
// side then reports an error per pair. With the redaction in the CLI, the
// maps go into SidedumpDir; render() then backfills and scrubs as for
// helmfile.
func (s *renderSide) renderNative(l engine.Layout) (int, error, error) {
	pairs, discErr := s.nativePairs(l)
	if discErr != nil {
		return 0, discErr, nil
	}
	s.pairs = pairs
	// the engine decrypts in this process: the side's key environment
	for _, kv := range s.extraEnv {
		k, v, _ := strings.Cut(kv, "=")
		if err := os.Setenv(k, v); err != nil {
			return 0, nil, err
		}
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.Timeout)
	defer cancel()
	o := native.Options{
		Layout: l, Pairs: pairs, OutputDir: s.snapshot, SkipSecrets: s.Keyless,
		Work: filepath.Join(s.Temp, s.Label+"-native"), Log: s.stderr, Phase: s.phase,
	}
	if s.RedactInCLI {
		o.MapsDir = s.SidedumpDir
	}
	res, err := native.Render(ctx, o)
	if err != nil {
		fmt.Fprintf(s.stderr, "%v\n", err)
		return len(pairs), err, nil
	}
	for _, p := range res.FailedPairs() {
		cluster, dep, _ := strings.Cut(p, "|")
		s.log.Annotate(s.level, "Render failed for %s/%s on %s", cluster, dep, s.sideName)
	}
	return len(pairs), res.Err(), nil
}

// nativePairs are the pairs of the side: the pairs file of a subset render,
// else the discovery map, else the engine's discovery.
func (s *renderSide) nativePairs(l engine.Layout) ([]string, error) {
	out := s.Outputs
	_ = out.Set("filter_supported", "true")
	if s.PairsFile != "" {
		data, err := os.ReadFile(s.PairsFile)
		if err != nil {
			s.log.Annotate("error", "RENDER_PAIRS_FILE not found: %s", s.PairsFile)
			_ = out.Set("status", "error")
			return nil, errDiscovery
		}
		pairs := []string{}
		seen := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "|") && !seen[line] {
				seen[line] = true
				pairs = append(pairs, line)
			}
		}
		s.log.Printf("Subset render: %d pair(s) from %s", len(pairs), s.PairsFile)
		return pairs, nil
	}
	entries, ok := s.pairsFromMap()
	if ok {
		s.phase("pairs from the discovery map")
	} else {
		s.phase("discovery (native engine)")
		found, err := engine.Discover(l, engine.Filter{})
		if err != nil {
			fmt.Fprintf(s.stderr, "%v\n", err)
			s.discoveryFailed()
			return nil, errDiscovery
		}
		for _, p := range found {
			entries = append(entries, listEntry{Name: p.DeploymentName, Labels: "cluster:" + p.Cluster + ",deploymentName:" + p.DeploymentName})
		}
	}
	if err := writeJSON(s.listJSON, entries); err != nil {
		return nil, err
	}
	_ = out.Set("list_json", s.listJSON)
	return append([]string{}, pairsOf(entries)...), nil
}
