package ci

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gha"
	"github.com/max06/atlas/internal/helmfile"
	"github.com/max06/atlas/internal/report"
	"github.com/max06/atlas/internal/snapshot"
)

// ExitError carries a step's exit code (discover: 2 = no map).
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// ── discover ────────────────────────────────────────────────────────────────

// Discover writes the discovery map of the tree in dir whose entry helmfile
// is entry (discover.sh): exit 0 = written, 2 = the tree has no map (entry
// missing, or an ATLAS without ATLAS_DISCOVERY_MAP — the caller renders in
// full), 1 = helmfile failed (the tree does not build).
func Discover(ctx context.Context, dir, entry, binary, out string, log io.Writer) error {
	if dir == "" {
		dir = "."
	}
	if l, native, err := nativeLayout(dir); err != nil {
		return &ExitError{1, "discover: " + err.Error()}
	} else if native {
		return discoverNative(l, out, log)
	}
	if _, err := os.Stat(inDir(dir, entry)); err != nil {
		return &ExitError{2, "discover: helmfile not found at " + entry}
	}
	var stderr strings.Builder
	r := helmfile.Runner{Binary: binary, File: entry, Dir: dir, Stderr: &stderr}
	if _, err := discovery.Probe(ctx, r); err != nil {
		if errors.Is(err, discovery.ErrUnsupported) {
			return &ExitError{2, "discover: no discovery map in build output (ATLAS without ATLAS_DISCOVERY_MAP support)"}
		}
		return &ExitError{1, "discover: helmfile build failed (probe)\n" + stderr.String()}
	}
	stderr.Reset()
	stream, err := r.Output(ctx, helmfile.Options{DiscoveryMap: true}, "build", "--allow-no-matching-release")
	if err != nil {
		return &ExitError{1, "discover: helmfile build failed\n" + stderr.String()}
	}
	raw, err := rawMap(stream)
	if err != nil {
		return &ExitError{1, "discover: " + err.Error()}
	}
	if raw == nil {
		return &ExitError{2, "discover: probe succeeded but the unfiltered build carried no map — treating as unsupported"}
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	var m discovery.Map
	_ = json.Unmarshal(raw, &m)
	fmt.Fprintf(log, "discover: %d pairs → %s\n", len(m.Pairs), out)
	return nil
}

// rawMap returns the atlasDiscovery value of a build stream as JSON, keeping
// every field ATLAS wrote (the struct in package discovery knows only what
// the CLI reads).
func rawMap(stream []byte) ([]byte, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(stream)))
	for {
		var state struct {
			RenderedValues struct {
				AtlasDiscovery any `yaml:"atlasDiscovery"`
			} `yaml:"renderedvalues"`
		}
		err := dec.Decode(&state)
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse helmfile build output: %w", err)
		}
		if v := state.RenderedValues.AtlasDiscovery; v != nil {
			return json.Marshal(v)
		}
	}
}

// ── redaction maps (ATLAS_REDACT_MAPS) ──────────────────────────────────────

// WriteRedactMaps reads the carrier states out of a `helmfile build` stream
// in ATLAS_REDACT_MAPS mode and writes one map file per release
// (snapshot.WriteMaps). It returns the number of releases written.
func WriteRedactMaps(stream []byte, mapDir string) (int, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(stream)))
	var all []snapshot.Maps
	for {
		var state struct {
			RenderedValues struct {
				Maps *snapshot.Maps `yaml:"atlasRedactMaps"`
			} `yaml:"renderedvalues"`
		}
		err := dec.Decode(&state)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("parse helmfile build output: %w", err)
		}
		if state.RenderedValues.Maps != nil {
			all = append(all, *state.RenderedValues.Maps)
		}
	}
	return snapshot.WriteMaps(mapDir, all)
}

// ── scrub / replay ──────────────────────────────────────────────────────────

// Scrub runs the redaction replay over a baseline tree and prints one status
// line per release (scrub-baseline.sh).
func Scrub(baselineRoot, mapDir string, stdout, stderr io.Writer) ([]snapshot.ScrubStatus, error) {
	statuses, err := snapshot.Scrub(baselineRoot, mapDir, nil, stderr)
	if err != nil {
		return nil, &ExitError{2, err.Error()}
	}
	for _, s := range statuses {
		fmt.Fprintln(stdout, s)
	}
	return statuses, nil
}

// ReplayConfig is the replay.sh contract.
type ReplayConfig struct {
	BaselineDir, SidedumpDir, Temp string
	Pairs                          []string // exact release paths (nil: guessed)
	Outputs                        gha.Outputs
	Log                            io.Writer
}

// Replay scrubs the baseline with the PR side's maps and writes
// <Temp>/replay-status.txt for the diff (replay.sh). Without a map
// directory (the PR side failed before it reported one) every baseline
// release counts as no-map, so the comment still reports the failed render.
func Replay(c ReplayConfig) ([]snapshot.ScrubStatus, error) {
	statusFile := filepath.Join(c.Temp, "replay-status.txt")
	if err := os.WriteFile(statusFile, nil, 0o644); err != nil {
		return nil, err
	}
	if st, err := os.Stat(c.BaselineDir); c.BaselineDir == "" || err != nil || !st.IsDir() {
		fmt.Fprintln(c.Log, "No baseline output to replay against — skipping.")
		_ = c.Outputs.Set("scrubbed_count", "0")
		return nil, c.Outputs.Set("no_map_count", "0")
	}
	mapDir := c.SidedumpDir
	if st, err := os.Stat(mapDir); mapDir == "" || err != nil || !st.IsDir() {
		fmt.Fprintln(c.Log, "::warning::No PR-side redaction maps captured — baseline diffs will be suppressed to prevent leaks from older ATLAS versions.")
		if mapDir == "" {
			mapDir = filepath.Join(c.Temp, "sidedump-maps-empty")
		}
		if err := os.MkdirAll(mapDir, 0o755); err != nil {
			return nil, err
		}
	}
	statuses, err := snapshot.Scrub(c.BaselineDir, mapDir, c.Pairs, c.Log)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	scrubbed, noMap := 0, 0
	for _, s := range statuses {
		b.WriteString(s.String() + "\n")
		switch s.Status {
		case "scrubbed":
			scrubbed++
		case "no-map", "unscrubbed":
			noMap++
		}
	}
	fmt.Fprint(c.Log, b.String())
	if err := os.WriteFile(statusFile, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	_ = c.Outputs.Setf("scrubbed_count", "%d", scrubbed)
	return statuses, c.Outputs.Setf("no_map_count", "%d", noMap)
}

// ReadReplayStatus reads replay-status.txt: the releases reported no-map,
// and those reported unscrubbed.
func ReadReplayStatus(path string) (noMap, unscrubbed map[string]bool) {
	noMap, unscrubbed = map[string]bool{}, map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return noMap, unscrubbed
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		status, rel, ok := strings.Cut(sc.Text(), " ")
		switch {
		case ok && status == "no-map":
			noMap[rel] = true
		case ok && status == "unscrubbed":
			unscrubbed[rel] = true
		}
	}
	return noMap, unscrubbed
}

// ── diff ────────────────────────────────────────────────────────────────────

// DiffConfig is the diff.sh contract.
type DiffConfig struct {
	BaselineDir, PRDir, Temp string
	Mode                     string
	SidedumpDir              string
	ReplayStatusFile         string
	Pairs                    []string // exact release paths (nil: guessed)
	Outputs                  gha.Outputs
}

// DiffOutcome is what the comment needs from the diff step.
type DiffOutcome struct {
	Result snapshot.DiffResult
	Blocks report.DiffBlocks
}

// Diff compares the two render trees and writes comment-diff.md,
// summary-diff.md, affected.md and affected-paths.txt into Temp plus the
// step outputs status, total, releases, suppressed_nomap,
// suppressed_removed, truncated (diff.sh).
func Diff(ctx context.Context, c DiffConfig) (DiffOutcome, error) {
	affectedPaths := filepath.Join(c.Temp, "affected-paths.txt")
	if err := os.WriteFile(affectedPaths, nil, 0o644); err != nil {
		return DiffOutcome{}, err
	}
	statusFile := c.ReplayStatusFile
	if statusFile == "" {
		statusFile = filepath.Join(c.Temp, "replay-status.txt")
	}
	noMap, unscrubbed := ReadReplayStatus(statusFile)
	res, err := snapshot.Diff(ctx, snapshot.DiffOptions{
		BaselineDir: c.BaselineDir, PRDir: c.PRDir, Mode: c.Mode,
		SidedumpDir: c.SidedumpDir, NoMap: noMap, Unscrubbed: unscrubbed, Pairs: c.Pairs,
	})
	if err != nil {
		return DiffOutcome{}, err
	}
	out := DiffOutcome{Result: res}
	o := c.Outputs
	switch res.Status {
	case snapshot.StatusEmpty:
		return out, o.Set("status", "empty")
	case snapshot.StatusNoChanges:
		return out, o.Set("status", "no-changes")
	}
	out.Blocks = report.Blocks(res)
	if err := os.WriteFile(affectedPaths, []byte(report.AffectedPaths(res)), 0o644); err != nil {
		return out, err
	}
	files := map[string]string{
		"comment-diff.md": out.Blocks.Comment + "\n",
		"summary-diff.md": out.Blocks.Summary + "\n",
		"affected.md":     out.Blocks.Affected + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(c.Temp, name), []byte(content), 0o644); err != nil {
			return out, err
		}
	}
	_ = o.Set("status", "changes")
	_ = o.Setf("total", "%d", res.Resources())
	_ = o.Setf("releases", "%d", len(res.Releases))
	_ = o.Setf("suppressed_nomap", "%d", res.Suppressed("no-map"))
	_ = o.Setf("suppressed_removed", "%d", res.Suppressed("removed"))
	if out.Blocks.Truncated {
		_ = o.Set("truncated", "true")
	}
	return out, nil
}

// ── shadow ──────────────────────────────────────────────────────────────────

// Shadow runs the shadow check on an affected-paths file and a classify.json
// (shadow-check.sh): writes shadow-check.md into outDir and the outputs
// shadow_result, shadow_missed.
func Shadow(affectedFile, classifyJSON, outDir string, o gha.Outputs, log io.Writer) (report.ShadowResult, error) {
	c, err := readClassify(classifyJSON)
	if err != nil {
		return report.ShadowResult{}, err
	}
	affected, err := readLines(affectedFile)
	if err != nil {
		return report.ShadowResult{}, err
	}
	res := report.Shadow(affected, c)
	if err := writeShadow(res, outDir, o, log); err != nil {
		return res, err
	}
	return res, nil
}

func writeShadow(res report.ShadowResult, outDir string, o gha.Outputs, log io.Writer) error {
	_ = o.Set("shadow_result", res.Result)
	_ = o.Setf("shadow_missed", "%d", len(res.Missed))
	if res.Markdown == "" {
		return nil
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "shadow-check.md"), []byte(res.Markdown), 0o644); err != nil {
		return err
	}
	switch res.Result {
	case report.ShadowCovered:
		fmt.Fprintln(log, "shadow-check: covered")
	case report.ShadowUncovered:
		fmt.Fprintf(log, "::warning::subset shadow check: %d changed deployment(s) outside the classifier's selection\n", len(res.Missed))
		for _, m := range res.Missed {
			fmt.Fprintln(log, m)
		}
	}
	return nil
}

func readClassify(path string) (classify.Result, error) {
	var c classify.Result
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(data, &c)
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// ── comment ─────────────────────────────────────────────────────────────────

// CommentFromEnv reads the comment.sh environment. Diff blocks and the
// scope fragment come from files in temp (comment-diff.md, summary-diff.md,
// scope.md), as comment.sh read them.
func CommentFromEnv(temp string) (report.CommentInput, error) {
	in := report.CommentInput{
		BaselineStatus:    os.Getenv("BASELINE_STATUS"),
		PRStatus:          os.Getenv("PR_STATUS"),
		DiffStatus:        os.Getenv("DIFF_STATUS"),
		MergeFallback:     os.Getenv("MERGE_FALLBACK") == "true",
		BaselineFilter:    os.Getenv("BASELINE_FILTER") != "false",
		PRFilter:          os.Getenv("PR_FILTER") != "false",
		WorkflowPinTarget: os.Getenv("WORKFLOW_PIN_TARGET"),
		WorkflowPinMerge:  os.Getenv("WORKFLOW_PIN_MERGE"),
		LatestTag:         os.Getenv("LATEST_ATLAS_TAG"),
		LatestMainSHA:     os.Getenv("LATEST_MAIN_SHA"),
		DiffTruncated:     os.Getenv("DIFF_TRUNCATED") == "true",
		HelmfilePath:      firstNonEmpty(os.Getenv("HELMFILE_PATH"), "helmfile.yaml.gotmpl"),
		RunURL:            os.Getenv("RUN_URL"),
	}
	for _, v := range []struct{ name, val string }{{"BASELINE_STATUS", in.BaselineStatus}, {"PR_STATUS", in.PRStatus}, {"DIFF_STATUS", in.DiffStatus}} {
		if v.val == "" {
			return in, fmt.Errorf("%s is required", v.name)
		}
	}
	in.DiffTotal, _ = strconv.Atoi(firstNonEmpty(os.Getenv("DIFF_TOTAL"), "0"))
	in.DiffReleases, _ = strconv.Atoi(firstNonEmpty(os.Getenv("DIFF_RELEASES"), "0"))
	in.BaselineErrors = logTail(firstNonEmpty(os.Getenv("BASELINE_STDERR_LOG"), filepath.Join(temp, "baseline-stderr.log")), 50)
	in.PRErrors = logTail(firstNonEmpty(os.Getenv("PR_STDERR_LOG"), filepath.Join(temp, "pr-stderr.log")), 50)
	in.DiffBlocks = readFile(filepath.Join(temp, "comment-diff.md"))
	in.SummaryBlocks = readFile(filepath.Join(temp, "summary-diff.md"))
	in.Scope = readFile(filepath.Join(temp, "scope.md"))
	return in, nil
}

// WriteComment writes the body output and <temp>/summary.md.
func WriteComment(in report.CommentInput, temp string, o gha.Outputs) error {
	if err := o.SetRaw("body", report.Comment(in)); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(temp, "summary.md"), []byte(report.Summary(in)), 0o644)
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// logTail returns the last n lines of a log, without trailing newlines.
func logTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	return tailLines(string(data), n)
}

func tailLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// progressLine matches the lines a render writes while it works; they never
// explain a failure.
var progressLine = regexp.MustCompile(`^(wrote |Templating release=|Adding repo |"?[\w.-]+"? has been added to your repositories|Building dependency|Pulling |Comparing |Listing |Hang tight|Update Complete|Successfully |::debug::)`)

// errorLine matches a line that explains a failure.
var errorLine = regexp.MustCompile(`(?i)(^|[^a-z])(error|fail|fatal|panic|exception|not found|forbidden|denied|timed? ?out|too long|invalid|cannot|could not)`)

// errorExcerpt returns the lines of a render log that explain its failure:
// the error lines, without progress output, consecutive duplicates folded
// into one line with a count (a parallel render repeats one error per pair).
// Without any error line the last n lines are returned, as before.
func errorExcerpt(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	var out []string
	last, count := "", 0
	flush := func() {
		if last == "" {
			return
		}
		if count > 1 {
			out = append(out, fmt.Sprintf("%s  (×%d)", last, count))
		} else {
			out = append(out, last)
		}
		last, count = "", 0
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if progressLine.MatchString(line) || !errorLine.MatchString(line) {
			continue
		}
		if line == last {
			count++
			continue
		}
		flush()
		last, count = line, 1
	}
	flush()
	if len(out) == 0 {
		return tailLines(string(data), n)
	}
	if len(out) > n {
		out = append(out[:n], fmt.Sprintf("… %d more error line(s), see the step log", len(out)-n))
	}
	return strings.Join(out, "\n")
}
