package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gha"
	"github.com/max06/atlas/internal/gitrev"
	"github.com/max06/atlas/internal/report"
	"github.com/max06/atlas/internal/snapshot"
)

// sharedCaches points helm and helmfile at /github/home: GitHub mounts the
// same directory into every docker step of a job, so the discovery steps
// fetch the remote states once and the baseline render reuses the charts the
// PR render downloaded. (The image defaults point into /cache, a volume per
// container.)
func sharedCaches() {
	if os.Getenv("GITHUB_ACTIONS") == "" {
		return
	}
	if st, err := os.Stat("/github/home"); err != nil || !st.IsDir() {
		return
	}
	os.Setenv("XDG_CACHE_HOME", "/github/home/.cache")
	os.Setenv("HELM_CACHE_HOME", "/github/home/.cache/helm")
	os.Setenv("HELM_CONFIG_HOME", "/github/home/.config/helm")
}

func input(name string) string { return os.Getenv("INPUT_" + name) }

// RenderAction is the atlas-render action: mode discover, classify or render
// from the INPUT_* environment GitHub sets for a docker action.
func RenderAction(ctx context.Context, binary string, log io.Writer) error {
	sharedCaches()
	entry := input("HELMFILE_PATH")
	label := input("SNAPSHOT_LABEL")
	if entry == "" || label == "" {
		return errors.New("inputs helmfile-path and snapshot-label are required")
	}
	temp := firstNonEmpty(os.Getenv("RUNNER_TEMP"), "/tmp")
	out := gha.FromEnv()
	l := gha.Log{W: log}

	switch firstNonEmpty(input("MODE"), "render") {
	case "discover":
		mapFile := filepath.Join(temp, label+"-map.json")
		err := Discover(ctx, ".", entry, binary, mapFile, log)
		var exit *ExitError
		switch {
		case err == nil:
			_ = out.Set("map_status", "ok")
			return out.Set("map_file", mapFile)
		case errors.As(err, &exit) && exit.Code == 2:
			fmt.Fprintln(log, exit.Msg)
			_ = out.Set("map_status", "unsupported")
			_ = out.Set("map_file", "")
			l.Annotate("notice", "No discovery map on the %s side — the pinned ATLAS predates ATLAS_DISCOVERY_MAP; falling back to a full render.", label)
			return nil
		default:
			fmt.Fprintln(log, err)
			_ = out.Set("map_status", "error")
			_ = out.Set("map_file", "")
			l.Annotate("warning", "Discovery map failed on the %s side — falling back to a full render (the render step reports the actual error).", label)
			return nil
		}

	case "classify":
		dir := filepath.Join(temp, "classify")
		changes := input("CHANGES_FILE")
		var r classify.Result
		if _, err := os.Stat(changes); changes == "" || err != nil {
			// no changed-file list = nothing to classify → default-deny
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "changes.txt"), nil, 0o644); err != nil {
				return err
			}
			r = classify.Result{Mode: "full", Reason: "changed-file list unavailable", Changes: []classify.Change{}, Selected: []string{}}
		} else {
			in, err := ClassifyInput(input("MAP_BASELINE"), input("MAP_PR"), changes, entry)
			if err != nil {
				return err
			}
			r = classify.Classify(in)
		}
		if err := classify.WriteFiles(dir, r, out.Path); err != nil {
			return err
		}
		fmt.Fprintf(log, "classify: mode=%s%s\n", r.Mode, paren(r.Reason))
		return out.Set("classify_dir", dir)

	case "render":
		c := RenderConfig{
			HelmfilePath:  entry,
			Label:         label,
			Temp:          temp,
			Blocking:      firstNonEmpty(input("BLOCKING"), "true") == "true",
			PairsFile:     input("PAIRS_FILE"),
			MapFile:       input("MAP_FILE"),
			MergeFallback: input("MERGE_FALLBACK") == "true",
			SopsAgeKey:    input("SOPS_AGE_KEY"),
			// redaction in the CLI (maps from a helmfile build in
			// ATLAS_REDACT_MAPS mode) instead of the helm post-renderer
			RedactInCLI: input("REDACT_IN_CLI") == "true",
			Helmfile:    binary,
			Outputs:     out,
			Log:         log,
		}
		var err error
		if c.Timeout, c.Parallel, err = renderTuning(); err != nil {
			return err
		}
		c.SopsAgeKeyFile = os.Getenv("SOPS_AGE_KEY_FILE")
		if input("ENABLE_SIDEDUMP") == "true" {
			c.SidedumpDir = filepath.Join(temp, "sidedump-maps")
			if err := os.MkdirAll(c.SidedumpDir, 0o755); err != nil {
				return err
			}
		}
		_, err = Render(ctx, c)
		return err
	default:
		return fmt.Errorf("unknown mode %q", input("MODE"))
	}
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

// ClassifyInput reads precomputed classification inputs: two discovery maps
// (missing or empty = that side has no map) and a name-status list.
func ClassifyInput(mapBase, mapPR, changesFile, entry string) (classify.Input, error) {
	in := classify.Input{Entry: entry}
	var err error
	if in.Base, err = ReadMap(mapBase); err != nil {
		return in, err
	}
	if in.PR, err = ReadMap(mapPR); err != nil {
		return in, err
	}
	data, err := os.ReadFile(changesFile)
	if err != nil {
		return in, err
	}
	in.Changes = gitrev.ParseNameStatus(string(data))
	return in, nil
}

// DiffAction is the atlas-diff action: replay → diff → render scope and
// shadow check → comment, from the INPUT_* environment.
func DiffAction(ctx context.Context, log io.Writer) error {
	temp := firstNonEmpty(os.Getenv("RUNNER_TEMP"), "/tmp")
	in := CompareInput{
		BaselineDir:    input("BASELINE_DIR"),
		PRDir:          input("PR_DIR"),
		SidedumpDir:    input("SIDEDUMP_MAP_DIR"),
		BaselineStatus: firstNonEmpty(input("BASELINE_STATUS"), "success"),
		PRStatus:       firstNonEmpty(input("PR_STATUS"), "success"),
		BaselineStderr: firstNonEmpty(input("BASELINE_STDERR_LOG"), filepath.Join(temp, "baseline-stderr.log")),
		PRStderr:       firstNonEmpty(input("PR_STDERR_LOG"), filepath.Join(temp, "pr-stderr.log")),
		MergeFallback:  input("MERGE_FALLBACK") == "true",
		BaselineFilter: firstNonEmpty(input("BASELINE_FILTER"), "true") != "false",
		PRFilter:       firstNonEmpty(input("PR_FILTER"), "true") != "false",
		PinTarget:      input("WORKFLOW_PIN_TARGET"),
		PinMerge:       input("WORKFLOW_PIN_MERGE"),
		LatestTag:      input("LATEST_ATLAS_TAG"),
		LatestMainSHA:  input("LATEST_MAIN_SHA"),
		DiffMode:       firstNonEmpty(input("DIFF_MODE"), "dyff"),
		SubsetMode:     firstNonEmpty(input("SUBSET_MODE"), "off"),
		RenderMode:     firstNonEmpty(input("RENDER_MODE"), "full"),
		HelmfilePath:   firstNonEmpty(input("HELMFILE_PATH"), "helmfile.yaml.gotmpl"),
		RunURL:         input("RUN_URL"),
		Temp:           temp,
	}
	in.Pairs = renderedPairs(filepath.Join(temp, "pr-pairs.txt"), filepath.Join(temp, "baseline-pairs.txt"))
	if dir := input("CLASSIFY_DIR"); in.SubsetMode != "off" && dir != "" {
		if c, err := readClassify(filepath.Join(dir, "classify.json")); err == nil {
			in.Classify = &c
			in.ClassifySummary = readFile(filepath.Join(dir, "classify-summary.md"))
		}
	}
	return Compare(ctx, in, gha.FromEnv(), log)
}

// CompareInput is what the second half of a review needs: both render
// trees, how they rendered, and what the comment states besides the diff.
type CompareInput struct {
	BaselineDir, PRDir, SidedumpDir string
	BaselineStatus, PRStatus        string // success / error / missing
	BaselineStderr, PRStderr        string // render logs (their tails go into the comment)
	MergeFallback                   bool
	BaselineFilter, PRFilter        bool // selector probe per side
	PinTarget, PinMerge             string
	EngineTarget, EngineMerge       string // atlas.yaml pins per side ("" = none)
	LatestTag, LatestMainSHA        string
	DiffMode                        string // dyff / classic
	SubsetMode                      string // off / shadow / on
	RenderMode                      string // full / subset (what the renders did)
	// Pairs of both renders ("<cluster>|<deployment>"): exact release paths
	// for the replay and the diff. nil = guessed from the trees.
	Pairs                []string
	Classify             *classify.Result
	ClassifySummary      string
	HelmfilePath, RunURL string
	Temp                 string
	// Keyless: both sides rendered without decryption; there are no redaction
	// maps to replay, nothing is suppressed, the comment says so.
	Keyless bool
}

// Compare replays the redaction maps over the baseline, diffs the trees,
// builds the render scope and the shadow check, and writes the comment
// (output body, <Temp>/summary.md) plus the step outputs status, total,
// releases, suppressed_*, truncated, scrubbed_count, no_map_count,
// shadow_result, shadow_missed and summary-file.
func Compare(ctx context.Context, in CompareInput, out gha.Outputs, log io.Writer) error {
	scratch := gha.Outputs{} // stage outputs that are not step outputs

	// 1. replay the PR side's redaction maps over the baseline (not keyless:
	//    nothing was decrypted, so there is nothing to scrub)
	var statuses []snapshot.ScrubStatus
	if in.Keyless {
		if err := os.WriteFile(filepath.Join(in.Temp, "replay-status.txt"), nil, 0o644); err != nil {
			return err
		}
	} else {
		var err error
		statuses, err = Replay(ReplayConfig{BaselineDir: in.BaselineDir, SidedumpDir: in.SidedumpDir, Temp: in.Temp, Pairs: in.Pairs, Outputs: scratch, Log: log})
		if err != nil {
			return err
		}
	}

	// 2. diff
	d, err := Diff(ctx, DiffConfig{
		BaselineDir: in.BaselineDir, PRDir: in.PRDir, Temp: in.Temp,
		Mode: in.DiffMode, SidedumpDir: in.SidedumpDir,
		ReplayStatusFile: filepath.Join(in.Temp, "replay-status.txt"), Pairs: in.Pairs, Outputs: scratch,
	})
	if err != nil {
		return err
	}

	// 3. render scope + shadow check
	scope := ""
	if in.SubsetMode != "off" && in.Classify != nil {
		var shadow *report.ShadowResult
		scope, shadow = report.Scope(report.ScopeInput{
			SubsetMode: in.SubsetMode, RenderMode: in.RenderMode,
			PRStatus: in.PRStatus, BaselineStatus: in.BaselineStatus,
			Classify: in.Classify, ClassifySummary: in.ClassifySummary,
			Affected: affectedOf(d),
		})
		if shadow != nil {
			if err := writeShadow(*shadow, in.Temp, out, log); err != nil {
				return err
			}
		}
	}

	// 4. comment
	c := report.CommentInput{
		BaselineStatus:    in.BaselineStatus,
		PRStatus:          in.PRStatus,
		DiffStatus:        d.Result.Status,
		Keyless:           in.Keyless,
		MergeFallback:     in.MergeFallback,
		BaselineFilter:    in.BaselineFilter,
		PRFilter:          in.PRFilter,
		WorkflowPinTarget: in.PinTarget,
		WorkflowPinMerge:  in.PinMerge,
		EngineTarget:      in.EngineTarget,
		EngineMerge:       in.EngineMerge,
		LatestTag:         in.LatestTag,
		LatestMainSHA:     in.LatestMainSHA,
		DiffTotal:         d.Result.Resources(),
		DiffReleases:      len(d.Result.Releases),
		DiffTruncated:     d.Blocks.Truncated,
		DiffBlocks:        d.Blocks.Comment,
		Scope:             scope,
		HelmfilePath:      in.HelmfilePath,
		RunURL:            in.RunURL,
		BaselineErrors:    errorExcerpt(in.BaselineStderr, 50),
		PRErrors:          errorExcerpt(in.PRStderr, 50),
	}
	if d.Blocks.Summary != "" {
		c.SummaryBlocks = d.Blocks.Summary + "\n"
	}
	if err := WriteComment(c, in.Temp, out); err != nil {
		return err
	}

	// 5. step outputs
	_ = out.Set("status", d.Result.Status)
	if d.Result.Status == "changes" {
		_ = out.Setf("total", "%d", d.Result.Resources())
		_ = out.Setf("releases", "%d", len(d.Result.Releases))
		_ = out.Setf("suppressed_nomap", "%d", d.Result.Suppressed("no-map"))
		_ = out.Setf("suppressed_removed", "%d", d.Result.Suppressed("removed"))
		if d.Blocks.Truncated {
			_ = out.Set("truncated", "true")
		}
	}
	scrubbed, noMap := 0, 0
	for _, s := range statuses {
		switch s.Status {
		case "scrubbed":
			scrubbed++
		case "no-map":
			noMap++
		}
	}
	_ = out.Setf("scrubbed_count", "%d", scrubbed)
	_ = out.Setf("no_map_count", "%d", noMap)
	return out.Set("summary-file", filepath.Join(in.Temp, "summary.md"))
}

func affectedOf(d DiffOutcome) []string {
	var out []string
	for _, r := range d.Result.Releases {
		out = append(out, r.Path)
	}
	return out
}

// ReadMap reads a discovery map file; nil when path is empty, missing or
// empty (that side has no map → full render).
func ReadMap(path string) (*discovery.Map, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	var m discovery.Map
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// renderedPairs is the union of the pairs both render steps wrote, nil
// unless both files exist (a side that stopped before rendering has none —
// then the release paths are guessed).
func renderedPairs(files ...string) []string {
	out := []string{}
	for _, f := range files {
		lines, err := readLines(f)
		if err != nil {
			return nil
		}
		out = append(out, lines...)
	}
	return out
}

// unionPairs joins the pairs of both renders; nil when a side has none
// (it stopped before rendering: guess the release paths instead).
func unionPairs(a, b []string) []string {
	if a == nil || b == nil {
		return nil
	}
	return append(append([]string{}, a...), b...)
}
