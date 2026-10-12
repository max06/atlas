package ci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/max06/atlas/internal/classify"
	"github.com/max06/atlas/internal/discovery"
	"github.com/max06/atlas/internal/gha"
	"github.com/max06/atlas/internal/gitrev"
)

// ReviewConfig is a whole review in one step: both revisions come from one
// git checkout, nothing is checked out in between by the workflow.
type ReviewConfig struct {
	Repo  string // the checkout ("." in CI: the merge commit)
	Entry string // entry helmfile, repo-relative
	// Base is the baseline commit: the merge commit's first parent, or the
	// PR's merge-base when there is no merge ref. Head "" = HEAD.
	Base, Head string

	SubsetMode    string // off / shadow / on
	DiffMode      string // dyff / classic
	MergeFallback bool   // Head is the PR branch, not a merge result

	LatestTag, LatestMainSHA, RunURL string

	SopsAgeKey, SopsAgeKeyFile string
	Helmfile                   string
	Timeout                    time.Duration
	Parallel                   int

	// InPlace renders in the checkout itself, switching it between the two
	// commits (and back to Head at the end) — the CI checkout, as the
	// step-wise workflow did. Otherwise both commits are exported into a
	// workspace under Temp and the checkout is never touched.
	InPlace bool

	Temp    string // work directory (RUNNER_TEMP)
	Outputs gha.Outputs
	Log     io.Writer
}

// Review renders both sides and writes the comment.
//
// Both revisions are exported into ONE workspace directory, one after the
// other: rendered values embed the checkout path (atlas.cwd), so two paths
// would differ everywhere. Order: discovery of both sides (maps, seconds),
// classification, the merge result's render (its redaction maps scrub the
// baseline), the baseline's render, then Compare.
//
// Step outputs: everything Compare writes, plus pr_status, baseline_status,
// render_mode, classify_mode.
func Review(ctx context.Context, c ReviewConfig) error {
	l := gha.Log{W: c.Log}
	temp, err := filepath.Abs(c.Temp)
	if err != nil {
		return err
	}
	repo, err := gitrev.Open(ctx, c.Repo)
	if err != nil {
		return err
	}
	head := c.Head
	if head == "" {
		head = "HEAD"
	}
	if head, err = repo.Resolve(ctx, head); err != nil {
		return fmt.Errorf("head %q: %w", c.Head, err)
	}
	if c.Base == "" {
		return errors.New("no baseline commit (base)")
	}
	base, err := repo.Resolve(ctx, c.Base)
	if err != nil {
		return fmt.Errorf("baseline %q: %w (fetch it first)", c.Base, err)
	}
	l.Printf("review: baseline %s, merge result %s", short(base), short(head))

	// outputs of an earlier run in the same directory must not leak into this one
	for _, dir := range []string{"classify", "sidedump-maps"} {
		if err := os.RemoveAll(filepath.Join(temp, dir)); err != nil {
			return err
		}
	}
	ws := filepath.Join(temp, "atlas-review", "tree")
	export := func(rev string) error { return repo.Export(ctx, rev, ws) }
	if c.InPlace {
		ws = repo.Dir
		export = func(rev string) error { return repo.Checkout(ctx, rev) }
		defer func() { _ = repo.Checkout(context.WithoutCancel(ctx), head) }()
	}

	// ── discovery maps ──────────────────────────────────────────────────────
	maps := map[string]string{}
	var baseMap, headMap *discovery.Map
	for _, side := range []struct{ label, rev string }{{"baseline", base}, {"pr", head}} {
		if err := export(side.rev); err != nil {
			return err
		}
		file := filepath.Join(temp, side.label+"-map.json")
		err := Discover(ctx, ws, c.Entry, c.Helmfile, file, c.Log)
		var exit *ExitError
		switch {
		case err == nil:
			maps[side.label] = file
		case errors.As(err, &exit) && exit.Code == 2:
			l.Printf("%s", exit.Msg)
			l.Annotate("notice", "No discovery map on the %s side — the pinned ATLAS predates ATLAS_DISCOVERY_MAP; falling back to a full render.", side.label)
		case errors.As(err, &exit):
			l.Printf("%s", exit.Msg)
			l.Annotate("warning", "Discovery map failed on the %s side — falling back to a full render (the render reports the actual error).", side.label)
		default:
			return err
		}
	}
	if maps["baseline"] != "" {
		baseMap, _ = ReadMap(maps["baseline"])
	}
	if maps["pr"] != "" {
		headMap, _ = ReadMap(maps["pr"])
	}

	// ── classification ──────────────────────────────────────────────────────
	var cls *classify.Result
	classifyDir := filepath.Join(temp, "classify")
	if c.SubsetMode != "off" {
		changes, err := repo.Changes(ctx, base, head)
		if err != nil {
			return err
		}
		r := classify.Classify(classify.Input{Base: baseMap, PR: headMap, Entry: c.Entry, Changes: changes})
		if err := classify.WriteFiles(classifyDir, r, ""); err != nil {
			return err
		}
		cls = &r
		l.Printf("classify: mode=%s%s, %d changed path(s)", r.Mode, paren(r.Reason), len(changes))
		_ = c.Outputs.Set("classify_mode", r.Mode)
	}
	subset := c.SubsetMode == "on" && cls != nil && cls.Mode == "subset"

	// ── renders ─────────────────────────────────────────────────────────────
	scratch := gha.Outputs{Path: filepath.Join(temp, "render-outputs")}
	side := func(label, rev string, blocking bool) (RenderResult, error) {
		if err := export(rev); err != nil {
			return RenderResult{}, err
		}
		rc := RenderConfig{
			HelmfilePath: c.Entry, Dir: ws, Label: label, Temp: temp, Blocking: blocking,
			MapFile: maps[label], SopsAgeKey: c.SopsAgeKey, SopsAgeKeyFile: c.SopsAgeKeyFile,
			Timeout: c.Timeout, Parallel: c.Parallel, Helmfile: c.Helmfile,
			Outputs: scratch, Log: c.Log,
		}
		if subset {
			rc.PairsFile = filepath.Join(classifyDir, "pairs-"+label+".txt")
		}
		if label == "pr" {
			rc.SidedumpDir = filepath.Join(temp, "sidedump-maps")
			if err := os.MkdirAll(rc.SidedumpDir, 0o755); err != nil {
				return RenderResult{}, err
			}
			rc.MergeFallback = c.MergeFallback
		}
		return Render(ctx, rc)
	}
	pr, err := side("pr", head, true)
	if err != nil {
		return err
	}
	baseline, err := side("baseline", base, false)
	if err != nil {
		return err
	}
	_ = c.Outputs.Set("pr_status", pr.Status)
	_ = c.Outputs.Set("baseline_status", baseline.Status)
	_ = c.Outputs.Set("render_mode", pr.Mode)

	// ── compare + comment ───────────────────────────────────────────────────
	in := CompareInput{
		BaselineDir: baseline.SnapshotDir, PRDir: pr.SnapshotDir, SidedumpDir: pr.SidedumpDir,
		BaselineStatus: baseline.Status, PRStatus: pr.Status,
		BaselineStderr: baseline.StderrLog, PRStderr: pr.StderrLog,
		MergeFallback:  c.MergeFallback,
		BaselineFilter: baseline.FilterSupported, PRFilter: pr.FilterSupported,
		PinTarget: baseline.WorkflowPin, PinMerge: pr.WorkflowPin,
		LatestTag: c.LatestTag, LatestMainSHA: c.LatestMainSHA,
		DiffMode: firstNonEmpty(c.DiffMode, "dyff"), SubsetMode: c.SubsetMode, RenderMode: firstNonEmpty(pr.Mode, "full"),
		Classify: cls, HelmfilePath: c.Entry, RunURL: c.RunURL, Temp: temp,
	}
	if cls != nil {
		in.ClassifySummary = readFile(filepath.Join(classifyDir, "classify-summary.md"))
	}
	if err := Compare(ctx, in, c.Outputs, c.Log); err != nil {
		return err
	}
	if c.InPlace {
		return nil
	}
	return os.RemoveAll(filepath.Dir(ws))
}

func short(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// ReviewAction is the atlas-review action: the whole review in one step,
// from the INPUT_* environment.
func ReviewAction(ctx context.Context, binary string, log io.Writer) error {
	sharedCaches()
	trustCheckout()
	timeout, parallel, err := renderTuning()
	if err != nil {
		return err
	}
	return Review(ctx, ReviewConfig{
		Repo:           ".",
		Entry:          firstNonEmpty(input("HELMFILE_PATH"), "helmfile.yaml.gotmpl"),
		Base:           input("BASE"),
		SubsetMode:     subsetMode(input("SUBSET_MODE"), log),
		DiffMode:       firstNonEmpty(input("DIFF_MODE"), "dyff"),
		MergeFallback:  input("MERGE_FALLBACK") == "true",
		LatestTag:      input("LATEST_ATLAS_TAG"),
		LatestMainSHA:  input("LATEST_MAIN_SHA"),
		RunURL:         input("RUN_URL"),
		SopsAgeKey:     input("SOPS_AGE_KEY"),
		SopsAgeKeyFile: os.Getenv("SOPS_AGE_KEY_FILE"),
		Helmfile:       binary,
		InPlace:        os.Getenv("GITHUB_ACTIONS") != "" && input("IN_PLACE") != "false",
		Timeout:        timeout,
		Parallel:       parallel,
		Temp:           firstNonEmpty(os.Getenv("RUNNER_TEMP"), "/tmp"),
		Outputs:        gha.FromEnv(),
		Log:            log,
	})
}

func subsetMode(v string, log io.Writer) string {
	switch v {
	case "off", "shadow", "on":
		return v
	case "":
		return "off"
	}
	fmt.Fprintf(log, "::warning::render-subset=%q unknown — using off\n", v)
	return "off"
}

// trustCheckout lets git work on the checkout inside a docker action: the
// runner checked it out as another user, and git refuses repositories it
// does not own ("dubious ownership") unless they are marked safe.
func trustCheckout() {
	if os.Getenv("GITHUB_ACTIONS") == "" {
		return
	}
	os.Setenv("GIT_CONFIG_COUNT", "1")
	os.Setenv("GIT_CONFIG_KEY_0", "safe.directory")
	os.Setenv("GIT_CONFIG_VALUE_0", "*")
}
