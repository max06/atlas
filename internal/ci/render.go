// Package ci runs the review pipeline steps inside GitHub Actions: render
// one side, compare two renders, build the comment. Each step keeps the
// input/output contract of the bash script it replaces (environment in,
// GITHUB_OUTPUT and files out), so the action entrypoints and the workflow
// tests stay as they are.
package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/max06/atlas/internal/gha"
	"github.com/max06/atlas/internal/helmfile"
	"github.com/max06/atlas/internal/snapshot"
)

// RenderConfig is the render.sh contract.
type RenderConfig struct {
	HelmfilePath   string // entry helmfile (HELMFILE_PATH), relative to Dir
	Dir            string // the tree to render ("" = working directory)
	Label          string // "pr" or "baseline" (SNAPSHOT_LABEL): names the outputs
	Temp           string // output root (RENDER_TEMP, else RUNNER_TEMP, else /tmp)
	Blocking       bool   // render errors are ::error (PR side) instead of ::warning
	PairsFile      string // subset: "<cluster>|<deployment>" lines to render (RENDER_PAIRS_FILE)
	MapFile        string // full render: discovery map of this side (RENDER_MAP_FILE)
	SidedumpDir    string // capture redaction maps here (ATLAS_SIDEDUMP_MAP_DIR)
	MergeFallback  bool   // the merge ref was unavailable (MERGE_FALLBACK)
	SopsAgeKey     string // written to a key file unless SopsAgeKeyFile is set
	SopsAgeKeyFile string
	Timeout        time.Duration // per helmfile call (HELMFILE_TIMEOUT seconds, default 900)
	Parallel       int           // concurrent helmfile calls (RENDER_PARALLEL, default: CPUs)
	Helmfile       string        // helmfile binary
	Outputs        gha.Outputs
	Log            io.Writer
}

// RenderConfigFromEnv reads the render.sh environment.
func RenderConfigFromEnv() (RenderConfig, error) {
	c := RenderConfig{
		HelmfilePath:   os.Getenv("HELMFILE_PATH"),
		Label:          os.Getenv("SNAPSHOT_LABEL"),
		Temp:           firstNonEmpty(os.Getenv("RENDER_TEMP"), os.Getenv("RUNNER_TEMP"), "/tmp"),
		Blocking:       os.Getenv("BLOCKING") != "false",
		PairsFile:      os.Getenv("RENDER_PAIRS_FILE"),
		MapFile:        os.Getenv("RENDER_MAP_FILE"),
		SidedumpDir:    os.Getenv("ATLAS_SIDEDUMP_MAP_DIR"),
		MergeFallback:  os.Getenv("MERGE_FALLBACK") == "true",
		SopsAgeKey:     os.Getenv("SOPS_AGE_KEY"),
		SopsAgeKeyFile: os.Getenv("SOPS_AGE_KEY_FILE"),
		Outputs:        gha.FromEnv(),
		Log:            os.Stdout,
	}
	if c.HelmfilePath == "" {
		return c, errors.New("HELMFILE_PATH is required")
	}
	if c.Label == "" {
		return c, errors.New("SNAPSHOT_LABEL is required")
	}
	var err error
	c.Timeout, c.Parallel, err = renderTuning()
	return c, err
}

// renderTuning reads HELMFILE_TIMEOUT (seconds per helmfile call, default
// 900) and RENDER_PARALLEL (concurrent calls, default: one per CPU).
func renderTuning() (time.Duration, int, error) {
	timeout, parallel := 900*time.Second, runtime.NumCPU()
	if v := os.Getenv("HELMFILE_TIMEOUT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, 0, fmt.Errorf("HELMFILE_TIMEOUT: %w", err)
		}
		timeout = time.Duration(n) * time.Second
	}
	if v := os.Getenv("RENDER_PARALLEL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return 0, 0, fmt.Errorf("RENDER_PARALLEL: %q is not a positive number", v)
		}
		parallel = n
	}
	return timeout, parallel, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// The output directory template both renders share: release directories at
// <root>/<cluster…>/<deployment>/<release>.
const outputDirTemplate = helmfile.OutputDirTemplate

// The template flags of the review renders: the production pair (the root
// ApplicationSet renders with them), and no --args — render.sh passed none.
var templateFlags = []string{"--skip-schema-validation", "--include-crds"}

type renderSide struct {
	RenderConfig
	ctx       context.Context
	snapshot  string
	stderrLog string
	listJSON  string
	sideName  string // "merge result" / "target branch"
	level     string // error / warning
	log       gha.Log
	extraEnv  []string
	extraArgs []string // --allow-failed-releases, --skip-refresh

	stderr *os.File // the render log, O_APPEND: parallel calls append like bash's 2>>
	// filterSupported: release selectors work (selector probe); false only
	// for an ATLAS with hardcoded sub-helmfile selectors
	filterSupported bool
	// pairs: the "<cluster>|<deployment>" pairs this render covers — they make
	// the release paths of the tree exact (snapshot.Releases)
	pairs    []string
	phaseMu  sync.Mutex
	phaseTxt string
}

// Render renders one side of a review into <Temp>/<Label>-snapshot and writes
// the render.sh step outputs: status (success / error / missing),
// snapshot_dir, list_json, filter_supported, render_mode, rendered_pairs,
// sidedump_dir, merge_fallback, workflow_pin.
//
// A render error is not returned as an error: it is the status "error" (the
// baseline may be broken and the PR the fix; the workflow decides). Errors
// are returned for the step itself failing (unwritable output and the like).
func Render(ctx context.Context, c RenderConfig) (RenderResult, error) {
	res, err := render(ctx, c)
	res.StderrLog = filepath.Join(c.Temp, c.Label+"-stderr.log")
	return res, err
}

// RenderResult is what a render reports (the render.sh step outputs).
type RenderResult struct {
	Status          string // success / error / missing
	SnapshotDir     string
	Mode            string // full / subset
	RenderedPairs   int
	FilterSupported bool
	SidedumpDir     string // "" unless redaction maps were captured
	WorkflowPin     string
	Pairs           []string // "<cluster>|<deployment>" pairs rendered (also in <Temp>/<Label>-pairs.txt)
	StderrLog       string
}

func render(ctx context.Context, c RenderConfig) (RenderResult, error) {
	if c.Dir == "" {
		c.Dir = "."
	}
	res := RenderResult{FilterSupported: true}
	// helmfile runs in Dir: every path it gets must be absolute
	for _, p := range []*string{&c.Temp, &c.SidedumpDir} {
		if *p != "" {
			abs, err := filepath.Abs(*p)
			if err != nil {
				return res, err
			}
			*p = abs
		}
	}
	s := &renderSide{
		RenderConfig: c,
		ctx:          ctx,
		snapshot:     filepath.Join(c.Temp, c.Label+"-snapshot"),
		stderrLog:    filepath.Join(c.Temp, c.Label+"-stderr.log"),
		listJSON:     filepath.Join(c.Temp, c.Label+"-list.json"),
		sideName:     "target branch",
		level:        "warning",
		log:          gha.Log{W: c.Log},

		filterSupported: true,
		pairs:           []string{},
	}
	if c.Label == "pr" {
		s.sideName = "merge result"
	}
	if c.Blocking {
		s.level = "error"
	}
	if c.Parallel < 1 {
		s.Parallel = 2
	}
	if c.Timeout <= 0 {
		s.Timeout = 900 * time.Second
	}
	if err := os.MkdirAll(c.Temp, 0o755); err != nil {
		return res, err
	}

	// SOPS key from the action input → a file sops reads
	if c.SopsAgeKeyFile == "" && c.SopsAgeKey != "" {
		keyFile := filepath.Join(c.Temp, "sops-age-key.txt")
		if err := os.WriteFile(keyFile, []byte(c.SopsAgeKey), 0o600); err != nil {
			return res, err
		}
		s.extraEnv = append(s.extraEnv, "SOPS_AGE_KEY_FILE="+keyFile)
		s.log.Printf("SOPS key file: %d bytes", len(c.SopsAgeKey))
	}

	s.phase(fmt.Sprintf("start (%d parallel helmfile calls)", s.Parallel))
	stopHeartbeat := s.heartbeat()
	defer stopHeartbeat()

	if _, err := os.Stat(inDir(c.Dir, c.HelmfilePath)); err != nil {
		s.log.Printf("Helmfile not found at %s", c.HelmfilePath)
		res.Status, res.Pairs = "missing", []string{} // nothing rendered, nothing to guess
		return res, c.Outputs.Set("status", "missing")
	}

	// a render tree left from an earlier run would mix with this one (the
	// diff splits files in place: old split files next to new ones)
	if err := os.RemoveAll(s.snapshot); err != nil {
		return res, err
	}
	f, err := os.OpenFile(s.stderrLog, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return res, err
	}
	s.stderr = f
	defer f.Close()

	// --allow-failed-releases (helmfile >= 1.8): the other releases of a call
	// keep rendering when one fails; the call still fails.
	if help, _ := exec.CommandContext(ctx, s.helmfileBin(), "--help").CombinedOutput(); bytes.Contains(help, []byte("--allow-failed-releases")) {
		s.extraArgs = append(s.extraArgs, "--allow-failed-releases")
	}

	var renderErr error
	var mode string
	var rendered int
	if c.PairsFile != "" {
		mode = "subset"
		rendered, renderErr, err = s.subset()
	} else {
		mode = "full"
		rendered, renderErr, err = s.full()
	}
	res.FilterSupported = s.filterSupported
	if err != nil {
		return res, err
	}
	if errors.Is(renderErr, errDiscovery) {
		res.Status = "error"
		return res, nil // outputs written, nothing rendered
	}
	status := "success"
	if renderErr != nil {
		status = "error"
		label := strings.ToUpper(s.sideName[:1]) + s.sideName[1:]
		what := "render"
		if mode == "subset" {
			what = "subset render"
		}
		s.log.Annotate(s.level, "%s %s failed (phase: %s)", label, what, s.currentPhase())
		s.stderrTail()
	}
	res.Status, res.SnapshotDir, res.Mode, res.RenderedPairs = status, s.snapshot, mode, rendered
	res.Pairs = s.pairs
	if err := os.WriteFile(filepath.Join(c.Temp, c.Label+"-pairs.txt"), []byte(strings.Join(s.pairs, "\n")+"\n"), 0o644); err != nil {
		return res, err
	}
	out := c.Outputs
	if err := out.Set("status", status); err != nil {
		return res, err
	}
	_ = out.Set("snapshot_dir", s.snapshot)
	_ = out.Set("render_mode", mode)
	_ = out.Setf("rendered_pairs", "%d", rendered)

	// Releases without secrets get no side-dump (the redaction pipeline only
	// writes a non-empty map). The replay reads a missing map as "older
	// ATLAS — suppress the diff", so an empty map ({}) marks them safe.
	if c.SidedumpDir != "" {
		if _, err := os.Stat(s.snapshot); err == nil {
			releases, err := snapshot.Releases(s.snapshot, s.pairs)
			if err != nil {
				return res, err
			}
			for _, r := range releases {
				mapFile := filepath.Join(c.SidedumpDir, filepath.FromSlash(r)+".json")
				if _, err := os.Stat(mapFile); err == nil {
					continue
				}
				if snapshot.MapAmbiguous(c.SidedumpDir, r) {
					// maps above or below: not a release of this render — an
					// empty map would unlock its baseline for the diff
					continue
				}
				if err := os.MkdirAll(filepath.Dir(mapFile), 0o755); err != nil {
					return res, err
				}
				if err := os.WriteFile(mapFile, []byte("{}\n"), 0o644); err != nil {
					return res, err
				}
			}
			res.SidedumpDir = c.SidedumpDir
			_ = out.Set("sidedump_dir", c.SidedumpDir)
		}
	}
	if c.MergeFallback {
		_ = out.Set("merge_fallback", "true")
	}
	pin := WorkflowPin(c.Dir)
	res.WorkflowPin = pin
	_ = out.Set("workflow_pin", pin)
	shown := pin
	if shown == "" {
		shown = "<not detected>"
	}
	s.log.Printf("%s ATLAS pin: %s", strings.ToUpper(s.sideName[:1])+s.sideName[1:], shown)

	if st, err := os.Stat(s.stderrLog); err == nil && st.Size() > 0 {
		data, _ := os.ReadFile(s.stderrLog)
		s.log.Group(strings.ToUpper(s.sideName[:1])+s.sideName[1:]+" render output", func() {
			fmt.Fprint(c.Log, string(data))
		})
	}
	return res, nil
}

var errDiscovery = errors.New("discovery failed")

func (s *renderSide) helmfileBin() string {
	if s.Helmfile != "" {
		return s.Helmfile
	}
	return "helmfile"
}

func (s *renderSide) runner(w io.Writer) helmfile.Runner {
	return helmfile.Runner{Binary: s.Helmfile, File: s.HelmfilePath, Dir: s.Dir, Stderr: w, ExtraEnv: s.extraEnv}
}

// run runs one helmfile call with the time limit, stderr into the render log.
func (s *renderSide) run(timeout time.Duration, opts helmfile.Options, stdout io.Writer, args ...string) error {
	return s.exec(timeout, opts, stdout, s.stderr, args...)
}

// exec runs one helmfile call in its own process group: SIGTERM to the group
// at the time limit, SIGKILL 30 s later (a deadlocked helmfile ignores
// SIGTERM — seen with helmfile#2261; helm children die with it). A nil stdout
// or stderr is the null device; files are passed as they are (no pipe, so a
// child that outlives helmfile cannot hold the call open).
func (s *renderSide) exec(timeout time.Duration, opts helmfile.Options, stdout, stderr io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	if stderr == nil {
		devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer devnull.Close()
		stderr = devnull
	}
	cmd := s.runner(stderr).Command(ctx, opts, args...)
	cmd.Stdout = stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 30 * time.Second
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return fmt.Errorf("helmfile %s: no result after %s", args[0], timeout)
	}
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		// helmfile succeeded; a leftover child kept a pipe open
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = nil
	}
	if err != nil {
		return fmt.Errorf("helmfile %s: %w", args[0], err)
	}
	return nil
}

func (s *renderSide) templateArgs(selectors []string) []string {
	args := append([]string{"template"}, selectors...)
	args = append(args, templateFlags...)
	args = append(args, s.extraArgs...)
	return append(args, "--output-dir", s.snapshot, "--output-dir-template", outputDirTemplate)
}

// ── repositories ────────────────────────────────────────────────────────────

// Parallel helmfile calls must not run "helm repo add" themselves: every call
// rewrote the shared repositories.yaml, and a concurrent call then failed with
// "repo <name> not found". `helmfile repos` adds every repository once (in the
// background while discovery runs); the renders then use --skip-refresh.
type warmup struct {
	done chan error
	log  string
}

func (s *renderSide) startWarmup(opts helmfile.Options) *warmup {
	w := &warmup{done: make(chan error, 1), log: filepath.Join(s.Temp, s.Label+"-repos.log")}
	go func() {
		f, err := os.Create(w.log)
		if err != nil {
			w.done <- err
			return
		}
		defer f.Close()
		w.done <- s.exec(s.Timeout, opts, f, f, "repos")
	}()
	return w
}

func (s *renderSide) finishWarmup(w *warmup) {
	if w == nil {
		return
	}
	err := <-w.done
	data, _ := os.ReadFile(w.log)
	if err != nil {
		s.log.Annotate("warning", "Repository warm-up failed on %s — every render adds its repositories itself", s.sideName)
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > 10 {
			lines = lines[len(lines)-10:]
		}
		for _, l := range lines {
			s.log.Printf("%s", cut(l, 300))
		}
		return
	}
	s.extraArgs = append(s.extraArgs, "--skip-refresh")
	s.log.Printf("Repositories added once (%d new)", bytes.Count(data, []byte("has been added")))
}

// ── subset ──────────────────────────────────────────────────────────────────

// subset renders exactly the pairs of PairsFile: one call per cluster with
// that cluster's deployments in the stage-1 filter and one selector per pair
// (the release-level safety net). The pairs come from this side's discovery
// map, so discovery and the selector probe are skipped. An empty file renders
// nothing and succeeds (this side has no selected pair).
func (s *renderSide) subset() (int, error, error) {
	out := s.Outputs
	data, err := os.ReadFile(s.PairsFile)
	if err != nil {
		s.log.Annotate("error", "RENDER_PAIRS_FILE not found: %s", s.PairsFile)
		_ = out.Set("status", "error")
		return 0, errDiscovery, nil
	}
	if err := os.MkdirAll(s.snapshot, 0o755); err != nil {
		return 0, nil, err
	}
	byCluster := map[string][]string{}
	var clusters, deployments []string
	seenPair, seenDep := map[string]bool{}, map[string]bool{}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "|") {
			continue
		}
		count++
		if seenPair[line] {
			continue
		}
		seenPair[line] = true
		s.pairs = append(s.pairs, line)
		cluster, dep, _ := strings.Cut(line, "|")
		if _, ok := byCluster[cluster]; !ok {
			clusters = append(clusters, cluster)
		}
		byCluster[cluster] = append(byCluster[cluster], dep)
		if !seenDep[dep] {
			seenDep[dep] = true
			deployments = append(deployments, dep)
		}
	}
	s.log.Printf("Subset render: %d pair(s) from %s", count, s.PairsFile)
	_ = out.Set("filter_supported", "true")
	if count == 0 {
		return 0, nil, nil
	}
	sort.Strings(clusters)
	sortedDeps := append([]string{}, deployments...)
	sort.Strings(sortedDeps)
	s.phase("add repositories of the selection (helmfile repos)")
	s.finishWarmup(s.startWarmup(helmfile.Options{Clusters: clusters, Deployments: sortedDeps}))
	s.phase(fmt.Sprintf("subset render of %d pair(s)", count))
	jobs := make([]func() error, 0, len(clusters))
	for _, cluster := range clusters {
		deps := byCluster[cluster]
		jobs = append(jobs, func() error {
			var sel []string
			for _, d := range deps {
				sel = append(sel, "--selector", "cluster="+cluster+",deploymentName="+d)
			}
			opts := helmfile.Options{Clusters: []string{cluster}, Deployments: deps, Redact: true, SidedumpDir: s.SidedumpDir}
			if err := s.run(s.Timeout, opts, nil, s.templateArgs(sel)...); err != nil {
				s.log.Annotate(s.level, "Render failed for %s [%s] on %s", cluster, strings.Join(deps, ","), s.sideName)
				return err
			}
			return nil
		})
	}
	return count, s.parallel(jobs), nil
}

// ── full ────────────────────────────────────────────────────────────────────

type listEntry struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Labels    string `json:"labels"`
}

// full renders every pair of the side. The pairs come from the discovery map
// when the workflow built one; otherwise from a `helmfile build` of the tree
// (not `helmfile list`: it deadlocks above 100 states on helmfile 1.2–1.7),
// followed by the selector probe that tells whether release selectors work
// (older ATLAS versions with hardcoded sub-helmfile selectors fall back to
// one bulk render).
func (s *renderSide) full() (int, error, error) {
	out := s.Outputs
	var pairs []string
	filterSupported := true
	var w *warmup
	if entries, ok := s.pairsFromMap(); ok {
		s.phase("pairs from the discovery map")
		w = s.startWarmup(helmfile.Options{})
		if err := writeJSON(s.listJSON, entries); err != nil {
			return 0, nil, err
		}
		_ = out.Set("list_json", s.listJSON)
		_ = out.Set("filter_supported", "true")
		pairs = pairsOf(entries)
		s.pairs = pairs
	} else {
		s.phase("discovery (helmfile build)")
		var stream bytes.Buffer
		if err := s.run(s.Timeout, helmfile.Options{}, &stream, "build", "--embed-values=false"); err != nil {
			s.discoveryFailed()
			return 0, errDiscovery, nil
		}
		entries, err := listFromBuild(stream.Bytes())
		if err != nil {
			fmt.Fprintf(s.stderr, "%v\n", err)
			s.discoveryFailed()
			return 0, errDiscovery, nil
		}
		if err := writeJSON(s.listJSON, entries); err != nil {
			return 0, nil, err
		}
		_ = out.Set("list_json", s.listJSON)
		// the build cached the remote states: the warm-up can run next to the probe
		w = s.startWarmup(helmfile.Options{})
		s.phase("selector probe (helmfile list)")
		filterSupported = s.probe(len(entries))
		s.filterSupported = filterSupported
		_ = out.Setf("filter_supported", "%t", filterSupported)
		pairs = pairsOf(entries)
		s.pairs = pairs
	}
	s.finishWarmup(w)

	if !filterSupported {
		s.phase("bulk render")
		return len(pairs), s.run(s.Timeout, helmfile.Options{Redact: true, SidedumpDir: s.SidedumpDir}, nil, s.templateArgs(nil)...), nil
	}
	s.phase(fmt.Sprintf("render of %d pair(s)", len(pairs)))
	jobs := make([]func() error, 0, len(pairs))
	for _, p := range pairs {
		cluster, dep, _ := strings.Cut(p, "|")
		jobs = append(jobs, func() error {
			opts := helmfile.Options{Clusters: []string{cluster}, Deployments: []string{dep}, Redact: true, SidedumpDir: s.SidedumpDir}
			sel := []string{"--selector", "cluster=" + cluster + ",deploymentName=" + dep}
			if err := s.run(s.Timeout, opts, nil, s.templateArgs(sel)...); err != nil {
				s.log.Annotate(s.level, "Render failed for %s/%s on %s", cluster, dep, s.sideName)
				return err
			}
			return nil
		})
	}
	return len(pairs), s.parallel(jobs), nil
}

func (s *renderSide) discoveryFailed() {
	s.log.Annotate(s.level, "Failed to discover deployments on %s", s.sideName)
	_ = s.Outputs.Set("status", "error")
	data, _ := os.ReadFile(s.stderrLog)
	fmt.Fprint(s.Log, string(data))
}

// pairsFromMap reads the side's discovery map (the same pairs as the
// discovery build, tested in discovery-map.bats; a map also proves that the
// stage-1 filter and the selectors work).
func (s *renderSide) pairsFromMap() ([]listEntry, bool) {
	if s.MapFile == "" {
		return nil, false
	}
	data, err := os.ReadFile(s.MapFile)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return nil, false
	}
	var m struct {
		Pairs []struct {
			Cluster        string `json:"cluster"`
			DeploymentName string `json:"deploymentName"`
		} `json:"pairs"`
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || !bytes.HasPrefix(bytes.TrimSpace(raw["pairs"]), []byte("[")) {
		return nil, false
	}
	if json.Unmarshal(data, &m) != nil {
		return nil, false
	}
	entries := make([]listEntry, 0, len(m.Pairs))
	for _, p := range m.Pairs {
		entries = append(entries, listEntry{Name: p.DeploymentName, Labels: "cluster:" + p.Cluster + ",deploymentName:" + p.DeploymentName})
	}
	return entries, true
}

// listFromBuild turns a `helmfile build --embed-values=false` stream into the
// `helmfile list --output json` shape: one entry per release of every state
// with commonLabels, labels as sorted "k:v" pairs.
func listFromBuild(stream []byte) ([]listEntry, error) {
	dec := yaml.NewDecoder(bytes.NewReader(stream))
	entries := []listEntry{}
	for {
		var state struct {
			CommonLabels map[string]any `yaml:"commonLabels"`
			Releases     []struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"releases"`
		}
		err := dec.Decode(&state)
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse helmfile build output: %w", err)
		}
		if state.CommonLabels == nil {
			continue
		}
		keys := make([]string, 0, len(state.CommonLabels))
		for k := range state.CommonLabels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var labels []string
		for _, k := range keys {
			labels = append(labels, fmt.Sprintf("%s:%v", k, state.CommonLabels[k]))
		}
		for _, r := range state.Releases {
			entries = append(entries, listEntry{Name: r.Name, Namespace: r.Namespace, Labels: strings.Join(labels, ",")})
		}
	}
}

// pairsOf returns the unique "<cluster>|<deployment>" pairs of list entries.
func pairsOf(entries []listEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		var cluster, dep string
		for _, kv := range strings.Split(e.Labels, ",") {
			k, v, _ := strings.Cut(kv, ":")
			switch k {
			case "cluster":
				cluster = v
			case "deploymentName":
				dep = v
			}
		}
		p := cluster + "|" + dep
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// probe tests whether CLI --selector reaches the release-level commonLabels:
// a selector matching nothing must list zero releases. Consumers pinned to an
// ATLAS with hardcoded sub-helmfile selectors fail it.
func (s *renderSide) probe(total int) bool {
	var stdout bytes.Buffer
	_ = s.exec(300*time.Second, helmfile.Options{Redact: true}, &stdout, nil, "list",
		"--selector", "cluster=__atlas_probe_nonexistent__", "--output", "json", "--allow-no-matching-release")
	count := 0
	if out := bytes.TrimSpace(stdout.Bytes()); len(out) > 0 {
		var list []any
		if json.Unmarshal(out, &list) == nil {
			count = len(list)
		} else {
			count = total
		}
	}
	if count == 0 {
		return true
	}
	s.log.Annotate("warning", "helmfile --selector did not narrow releases on the %s side — falling back to bulk render.", s.sideName)
	s.log.Group("Probe diagnostic ("+s.Label+")", func() {
		s.log.Printf("PROBE_COUNT=%d (expected 0 if selector works)", count)
		s.log.Printf("PROBE_OUT (first 400 chars):")
		s.log.Printf("%s", cut(stdout.String(), 400))
	})
	return false
}

// ── helpers ─────────────────────────────────────────────────────────────────

// parallel runs jobs with at most Parallel at a time; every job runs, the
// errors are joined.
func (s *renderSide) parallel(jobs []func() error) error {
	sem := make(chan struct{}, s.Parallel)
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs[i] = job()
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (s *renderSide) phase(p string) {
	s.phaseMu.Lock()
	s.phaseTxt = p
	s.phaseMu.Unlock()
	s.log.Printf("[%s] %s: %s", time.Now().UTC().Format("15:04:05"), s.sideName, p)
}

func (s *renderSide) currentPhase() string {
	s.phaseMu.Lock()
	defer s.phaseMu.Unlock()
	return s.phaseTxt
}

// heartbeat prints the phase and the files written once a minute, so a slow
// render and a hanging one look different in the step log; after two minutes
// without a new file it lists the running processes.
func (s *renderSide) heartbeat() func() {
	stop := make(chan struct{})
	started := time.Now()
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		last, stalled := -1, 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			files := countFiles(s.snapshot)
			s.log.Printf("[heartbeat] %s: %ds, phase: %s, %d files written", s.sideName, int(time.Since(started).Seconds()), s.currentPhase(), files)
			if files == last {
				stalled++
			} else {
				stalled = 0
			}
			last = files
			if stalled >= 2 {
				s.log.Printf("[heartbeat] no progress for %d min — processes:", stalled)
				ps, _ := exec.Command("ps", "-o", "pid,ppid,etime,args").Output()
				for _, l := range strings.Split(strings.TrimRight(string(ps), "\n"), "\n") {
					s.log.Printf("[heartbeat]   %s", cut(l, 220))
				}
			}
		}
	}()
	return func() { close(stop) }
}

func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// stderrTail shows the end of the helmfile log (errors name files and keys,
// not values; still short).
func (s *renderSide) stderrTail() {
	data, _ := os.ReadFile(s.stderrLog)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	s.log.Group("Last lines of the helmfile log ("+s.sideName+")", func() {
		for _, l := range lines {
			s.log.Printf("%s", cut(l, 400))
		}
	})
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

var pinPattern = regexp.MustCompile(`uses:\s*max06/atlas/\.github/workflows/snapshot-review\.yml@(\S+)`)

// WorkflowPin is the ref the repository in dir pins the review workflow to
// (its .github/workflows/*.yml|yaml), "" when none is found.
func WorkflowPin(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml"))
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if m := pinPattern.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// inDir resolves p against dir (an absolute p stays as it is).
func inDir(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}
