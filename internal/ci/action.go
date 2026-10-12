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
		err := Discover(ctx, entry, binary, mapFile, log)
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
			Helmfile:      binary,
			Outputs:       out,
			Log:           log,
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
		return Render(ctx, c)
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
	out := gha.FromEnv()
	scratch := gha.Outputs{} // stage outputs that are not step outputs

	baselineStatus := firstNonEmpty(input("BASELINE_STATUS"), "success")
	prStatus := firstNonEmpty(input("PR_STATUS"), "success")

	// 1. replay the PR side's redaction maps over the baseline
	statuses, err := Replay(ReplayConfig{BaselineDir: input("BASELINE_DIR"), SidedumpDir: input("SIDEDUMP_MAP_DIR"), Temp: temp, Outputs: scratch, Log: log})
	if err != nil {
		return err
	}

	// 2. diff
	d, err := Diff(ctx, DiffConfig{
		BaselineDir: input("BASELINE_DIR"), PRDir: input("PR_DIR"), Temp: temp,
		Mode: firstNonEmpty(input("DIFF_MODE"), "dyff"), SidedumpDir: input("SIDEDUMP_MAP_DIR"),
		ReplayStatusFile: filepath.Join(temp, "replay-status.txt"), Outputs: scratch,
	})
	if err != nil {
		return err
	}

	// 3. render scope + shadow check
	scope := ""
	subsetMode := firstNonEmpty(input("SUBSET_MODE"), "off")
	classifyDir := input("CLASSIFY_DIR")
	if subsetMode != "off" && classifyDir != "" {
		if c, err := readClassify(filepath.Join(classifyDir, "classify.json")); err == nil {
			var shadow *report.ShadowResult
			scope, shadow = report.Scope(report.ScopeInput{
				SubsetMode: subsetMode, RenderMode: firstNonEmpty(input("RENDER_MODE"), "full"),
				PRStatus: prStatus, BaselineStatus: baselineStatus,
				Classify: &c, ClassifySummary: readFile(filepath.Join(classifyDir, "classify-summary.md")),
				Affected: affectedOf(d),
			})
			if shadow != nil {
				if err := writeShadow(*shadow, temp, out, log); err != nil {
					return err
				}
			}
		}
	}

	// 4. comment
	in := report.CommentInput{
		BaselineStatus:    baselineStatus,
		PRStatus:          prStatus,
		DiffStatus:        d.Result.Status,
		MergeFallback:     input("MERGE_FALLBACK") == "true",
		BaselineFilter:    firstNonEmpty(input("BASELINE_FILTER"), "true") != "false",
		PRFilter:          firstNonEmpty(input("PR_FILTER"), "true") != "false",
		WorkflowPinTarget: input("WORKFLOW_PIN_TARGET"),
		WorkflowPinMerge:  input("WORKFLOW_PIN_MERGE"),
		LatestTag:         input("LATEST_ATLAS_TAG"),
		LatestMainSHA:     input("LATEST_MAIN_SHA"),
		DiffTotal:         d.Result.Resources(),
		DiffReleases:      len(d.Result.Releases),
		DiffTruncated:     d.Blocks.Truncated,
		DiffBlocks:        d.Blocks.Comment,
		Scope:             scope,
		HelmfilePath:      firstNonEmpty(input("HELMFILE_PATH"), "helmfile.yaml.gotmpl"),
		RunURL:            input("RUN_URL"),
		BaselineErrors:    logTail(firstNonEmpty(input("BASELINE_STDERR_LOG"), filepath.Join(temp, "baseline-stderr.log")), 50),
		PRErrors:          logTail(firstNonEmpty(input("PR_STDERR_LOG"), filepath.Join(temp, "pr-stderr.log")), 50),
	}
	if d.Blocks.Summary != "" {
		in.SummaryBlocks = d.Blocks.Summary + "\n"
	}
	if err := WriteComment(in, temp, out); err != nil {
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
	return out.Set("summary-file", filepath.Join(temp, "summary.md"))
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
