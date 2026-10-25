package ci

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pinnedTree(t *testing.T, engine string) string {
	t.Helper()
	dir := t.TempDir()
	if engine != "" {
		content := "apiVersion: atlas.io/v1alpha1\nkind: Root\nspec:\n  engine: " + engine + "\n"
		if err := os.WriteFile(filepath.Join(dir, "atlas.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEngineFor(t *testing.T) {
	for name, tc := range map[string]struct {
		engine, version, repo string
		image                 string // "" = this binary
		err                   string
	}{
		"no atlas.yaml":       {"", "0.13.0", "", "", ""},
		"own minor":           {`"0.13"`, "v0.13.2", "", "", ""},
		"other minor":         {`"0.12"`, "0.13.0", "", "ghcr.io/max06/atlas:0.12", ""},
		"other repository":    {`"0.14"`, "0.13.0", "registry.example/fork/atlas", "registry.example/fork/atlas:0.14", ""},
		"development build":   {`"0.12"`, "dev", "", "", ""},
		"branch image":        {`"0.12"`, "feat-x", "", "", ""},
		"invalid atlas.yaml":  {`0.12`, "0.13.0", "", "", "must be a quoted string"},
		"unknown key in spec": {`"0.12"` + "\n  engines: x", "0.13.0", "", "", "field engines not found"},
	} {
		e, err := engineFor(pinnedTree(t, tc.engine), tc.version, tc.repo)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: got %v, want %q", name, err, tc.err)
			}
			continue
		}
		if err != nil || e.Image != tc.image {
			t.Errorf("%s: got %+v, %v; want image %q", name, e, err, tc.image)
		}
	}
}

func TestSideFilesRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	req := SideRequest{Contract: SideContract, Step: "discover", Dir: tmp, Entry: "helmfile.yaml.gotmpl", Label: "pr", Temp: tmp, MapFile: filepath.Join(tmp, "map.json")}
	reqFile, resFile := filepath.Join(tmp, "req.json"), filepath.Join(tmp, "res.json")
	data, _ := json.Marshal(req)
	if err := os.WriteFile(reqFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// no entry helmfile in the tree: discover reports "unsupported", the step itself succeeds
	if err := SideFiles(context.Background(), reqFile, resFile, "helmfile", "0.13.0", io.Discard); err != nil {
		t.Fatal(err)
	}
	var res SideResult
	data, _ = os.ReadFile(resFile)
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Contract != SideContract || res.Engine != "0.13.0" || res.MapStatus != "unsupported" || !strings.Contains(res.MapMessage, "helmfile not found") {
		t.Errorf("got %+v", res)
	}

	req.Contract = SideContract + 1
	data, _ = json.Marshal(req)
	_ = os.WriteFile(reqFile, data, 0o644)
	if err := SideFiles(context.Background(), reqFile, resFile, "helmfile", "0.13.0", io.Discard); err == nil || !strings.Contains(err.Error(), "speaks 1") {
		t.Errorf("newer contract: %v", err)
	}
}

// fakeDocker puts a docker script on PATH; it receives the docker arguments.
func fakeDocker(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDockerSide(t *testing.T) {
	tmp := t.TempDir()
	req := SideRequest{Contract: SideContract, Step: "render", Dir: tmp, Entry: "e", Label: "baseline", Temp: tmp}
	ctx := context.Background()

	// the result file is the last argument; an old release has no "side" command
	fakeDocker(t, `echo 'Error: unknown command "side" for "atlas review ci"' >&2; exit 1`)
	if _, err := dockerSide(ctx, "ghcr.io/max06/atlas:0.12", req, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot render a review side") {
		t.Errorf("old release: %v", err)
	}
	fakeDocker(t, `echo 'atlas: unknown flag: --request' >&2; exit 1`)
	if _, err := dockerSide(ctx, "ghcr.io/max06/atlas:0.13", req, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot render a review side") {
		t.Errorf("old release (flag form): %v", err)
	}

	fakeDocker(t, `for a; do last=$a; done; echo '{"contract": 2}' > "$last"`)
	if _, err := dockerSide(ctx, "ghcr.io/max06/atlas:0.20", req, io.Discard); err == nil || !strings.Contains(err.Error(), "side contract 2") {
		t.Errorf("newer contract: %v", err)
	}

	// a failed render still delivers its result
	fakeDocker(t, `for a; do last=$a; done; echo '{"contract": 1, "engine": "0.12.1", "render": {"status": "error", "snapshotDir": "/x", "mode": "full", "renderedPairs": 0, "filterSupported": true}}' > "$last"; exit 1`)
	res, err := dockerSide(ctx, "ghcr.io/max06/atlas:0.12", req, io.Discard)
	if err != nil || res.Render == nil || res.Render.Status != "error" || res.Engine != "0.12.1" {
		t.Errorf("failed render: %+v, %v", res, err)
	}

	// the arguments: same-path mounts, own user, the image, the side command
	fakeDocker(t, `for a; do last=$a; done; echo "$@" > "$(dirname "$last")/args"; echo '{"contract": 1, "mapStatus": "ok"}' > "$last"`)
	req.Step = "discover"
	if _, err := dockerSide(ctx, "ghcr.io/max06/atlas:0.12", req, io.Discard); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(tmp, "args"))
	for _, want := range []string{"--volume " + tmp + ":" + tmp, "--workdir " + tmp, "--user ", "ghcr.io/max06/atlas:0.12 review ci side --request " + tmp + "/side-baseline-discover.request.json"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("docker args %q lack %q", args, want)
		}
	}
}
