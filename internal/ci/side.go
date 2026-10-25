package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/max06/atlas/internal/config"
	"github.com/max06/atlas/internal/gha"
)

// SideContract is the version of the request and result files of
// `atlas review ci side`. A review renders each side with the engine that
// side pins in atlas.yaml; when that is another release than the reviewing
// binary, the two talk only through these files. Add fields, never change or
// remove them; a breaking change needs a new number, and the coordinator
// refuses results with a number it does not know.
const SideContract = 1

// DefaultEngineRepository is where pinned engines come from: the image
// repository plus the pin as the tag ("<repository>:0.13").
const DefaultEngineRepository = "ghcr.io/max06/atlas"

// SideRequest is one per-side step of a review. All paths are absolute and
// inside Dir or Temp: a dispatched engine sees exactly these two mounts.
type SideRequest struct {
	Contract int    `json:"contract"`
	Step     string `json:"step"` // "discover" or "render"
	Dir      string `json:"dir"`  // the tree of this side
	Entry    string `json:"entry"`
	Label    string `json:"label"` // "pr" or "baseline": names the outputs under Temp
	Temp     string `json:"temp"`
	// discover: where the map goes; render: the side's map (full render)
	MapFile        string `json:"mapFile,omitempty"`
	PairsFile      string `json:"pairsFile,omitempty"`
	SidedumpDir    string `json:"sidedumpDir,omitempty"`
	Blocking       bool   `json:"blocking,omitempty"`
	MergeFallback  bool   `json:"mergeFallback,omitempty"`
	Keyless        bool   `json:"keyless,omitempty"`
	RedactInCLI    bool   `json:"redactInCLI,omitempty"`
	SopsAgeKeyFile string `json:"sopsAgeKeyFile,omitempty"` // the key never travels in the request
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	Parallel       int    `json:"parallel,omitempty"`
}

// SideResult is what the step reports.
type SideResult struct {
	Contract int    `json:"contract"`
	Engine   string `json:"engine"` // version of the binary that ran the step
	// discover: "ok", "unsupported" (ATLAS without a discovery map) or
	// "error"; MapMessage says why
	MapStatus  string        `json:"mapStatus,omitempty"`
	MapMessage string        `json:"mapMessage,omitempty"`
	Render     *RenderResult `json:"render,omitempty"`
}

// RunSide runs one per-side step with this binary.
func RunSide(ctx context.Context, req SideRequest, binary, version string, log io.Writer) (SideResult, error) {
	res := SideResult{Contract: SideContract, Engine: version}
	if req.Contract != SideContract {
		return res, fmt.Errorf("side request contract %d, this atlas (%s) speaks %d", req.Contract, version, SideContract)
	}
	switch req.Step {
	case "discover":
		err := Discover(ctx, req.Dir, req.Entry, binary, req.MapFile, log)
		var exit *ExitError
		switch {
		case err == nil:
			res.MapStatus = "ok"
		case errors.As(err, &exit) && exit.Code == 2:
			res.MapStatus, res.MapMessage = "unsupported", exit.Msg
		case errors.As(err, &exit):
			res.MapStatus, res.MapMessage = "error", exit.Msg
		default:
			return res, err
		}
		return res, nil
	case "render":
		r, err := Render(ctx, RenderConfig{
			HelmfilePath: req.Entry, Dir: req.Dir, Label: req.Label, Temp: req.Temp, Blocking: req.Blocking,
			PairsFile: req.PairsFile, MapFile: req.MapFile, SidedumpDir: req.SidedumpDir,
			MergeFallback: req.MergeFallback, SopsAgeKeyFile: req.SopsAgeKeyFile,
			Keyless: req.Keyless, RedactInCLI: req.RedactInCLI,
			Timeout: time.Duration(req.TimeoutSeconds) * time.Second, Parallel: req.Parallel,
			Helmfile: binary,
			Outputs:  gha.Outputs{Path: filepath.Join(req.Temp, "render-outputs")},
			Log:      log,
		})
		res.Render = &r
		return res, err
	}
	return res, fmt.Errorf("side request: unknown step %q", req.Step)
}

// SideFiles runs a request file and writes the result file: the entry point
// of a dispatched engine (`atlas review ci side`).
func SideFiles(ctx context.Context, requestFile, resultFile, binary, version string, log io.Writer) error {
	data, err := os.ReadFile(requestFile)
	if err != nil {
		return err
	}
	var req SideRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("%s: %w", requestFile, err)
	}
	res, runErr := RunSide(ctx, req, binary, version, log)
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(resultFile, append(out, '\n'), 0o644); err != nil {
		return err
	}
	return runErr
}

// sideEngine is who runs the steps of one side.
type sideEngine struct {
	Pin   string // "" = no atlas.yaml
	Image string // "" = this binary
	Note  string // why, for the log
}

func (e sideEngine) String() string {
	if e.Image == "" {
		return "this atlas"
	}
	return e.Image
}

// engineFor decides who renders the tree in dir. No atlas.yaml: this binary,
// as before pins existed. A pin of this binary's minor: this binary. Another
// pin: the release image of that minor. A build without a release version
// (branch image, local build) renders every side itself: it is the engine
// under test.
func engineFor(dir, version, repository string) (sideEngine, error) {
	root, err := config.Load(dir)
	if errors.Is(err, config.ErrNotFound) {
		return sideEngine{Note: "no " + config.FileName}, nil
	}
	if err != nil {
		return sideEngine{}, err
	}
	pin := root.Spec.Engine
	own, ok := config.EngineOfVersion(version)
	switch {
	case !ok:
		return sideEngine{Pin: pin.String(), Note: fmt.Sprintf("pins %s, development build %s renders it", pin, version)}, nil
	case own.Major == pin.Major && own.Minor == pin.Minor:
		return sideEngine{Pin: pin.String(), Note: "pins " + pin.String()}, nil
	}
	return sideEngine{Pin: pin.String(), Image: firstNonEmpty(repository, DefaultEngineRepository) + ":" + pin.String(), Note: "pins " + pin.String()}, nil
}

// dockerSide runs a side step in the pinned release image. The image sees
// the tree and the temp directory at their own paths (rendered values embed
// the tree path, so both sides must render from the same path) and runs as
// this process's user, so the files it writes stay ours.
func dockerSide(ctx context.Context, image string, req SideRequest, log io.Writer) (SideResult, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return SideResult{}, fmt.Errorf("the %s side pins %s: this review needs docker to run it (in the action: mount the docker socket)", req.Label, image)
	}
	base := filepath.Join(req.Temp, "side-"+req.Label+"-"+req.Step)
	reqFile, resFile := base+".request.json", base+".result.json"
	_ = os.Remove(resFile)
	data, err := json.Marshal(req)
	if err != nil {
		return SideResult{}, err
	}
	if err := os.WriteFile(reqFile, data, 0o644); err != nil {
		return SideResult{}, err
	}
	// one HOME per engine: helm and helmfile caches of two releases stay apart
	home := filepath.Join(req.Temp, "atlas-home-"+strings.NewReplacer("/", "_", ":", "_").Replace(image))
	if err := os.MkdirAll(home, 0o755); err != nil {
		return SideResult{}, err
	}
	args := []string{"run", "--rm", "--pull", "missing",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--workdir", req.Dir,
		"--volume", req.Dir + ":" + req.Dir,
		"--volume", req.Temp + ":" + req.Temp,
		"--env", "HOME=" + home,
	}
	if req.SopsAgeKeyFile != "" && !within(req.SopsAgeKeyFile, req.Temp) && !within(req.SopsAgeKeyFile, req.Dir) {
		args = append(args, "--volume", req.SopsAgeKeyFile+":"+req.SopsAgeKeyFile+":ro")
	}
	for _, name := range []string{"GITHUB_ACTIONS", "CI", "RUNNER_DEBUG", "ACTIONS_STEP_DEBUG"} {
		if os.Getenv(name) != "" {
			args = append(args, "--env", name)
		}
	}
	args = append(args, image, "review", "ci", "side", "--request", reqFile, "--result", resFile)
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = log, io.MultiWriter(log, &stderr)
	runErr := cmd.Run()
	data, err = os.ReadFile(resFile)
	if err != nil {
		// a release without the command: cobra names the command or, when
		// the parent has subcommands, the first flag it does not know
		if e := stderr.String(); strings.Contains(e, `unknown command "side"`) || strings.Contains(e, "unknown flag: --request") {
			return SideResult{}, fmt.Errorf("the %s side pins %s, but that release cannot render a review side (no `review ci side`, contract %d)", req.Label, image, SideContract)
		}
		if runErr != nil {
			return SideResult{}, fmt.Errorf("the %s side pins %s: %w", req.Label, image, runErr)
		}
		return SideResult{}, fmt.Errorf("the %s side pins %s: no result file: %w", req.Label, image, err)
	}
	var res SideResult
	if err := json.Unmarshal(data, &res); err != nil {
		return res, fmt.Errorf("%s: %w", resFile, err)
	}
	if res.Contract != SideContract {
		return res, fmt.Errorf("the %s side pins %s, which answers with side contract %d; this atlas reads %d", req.Label, image, res.Contract, SideContract)
	}
	// a failed render still reports its result (status error); only a step
	// without a result is fatal
	if runErr != nil && res.Render == nil && res.MapStatus == "" {
		return res, fmt.Errorf("the %s side pins %s: %w", req.Label, image, runErr)
	}
	return res, nil
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}
