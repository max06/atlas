package gitrev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo builds a throwaway repository with a commit on main.
func repo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a\n")
	write(t, dir, ".gitignore", "ignored.txt\n")
	commit(t, dir, "base")
	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@localhost", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@localhost",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	run(t, dir, "add", "-A")
	run(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg)
}

func TestSnapshotLeavesIndexAndStatusAlone(t *testing.T) {
	r := repo(t)
	ctx := context.Background()
	write(t, r.Dir, "a.txt", "a changed\n")
	write(t, r.Dir, "new.txt", "untracked\n")
	write(t, r.Dir, "ignored.txt", "ignored\n")
	before := run(t, r.Dir, "status", "--porcelain")

	snap, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after := run(t, r.Dir, "status", "--porcelain"); after != before {
		t.Fatalf("status changed:\nbefore %q\nafter  %q", before, after)
	}
	changes, err := r.Changes(ctx, "HEAD", snap)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Status
	}
	if got["a.txt"] != "M" || got["new.txt"] != "A" {
		t.Fatalf("snapshot misses working-tree changes: %v", got)
	}
	if _, ok := got["ignored.txt"]; ok {
		t.Fatalf("snapshot includes an ignored file: %v", got)
	}
	if parent := run(t, r.Dir, "rev-parse", snap+"^"); parent != run(t, r.Dir, "rev-parse", "HEAD") {
		t.Fatalf("snapshot parent %s is not HEAD", parent)
	}
}

func TestMergeTreeIncludesNewerBaseCommits(t *testing.T) {
	r := repo(t)
	ctx := context.Background()
	run(t, r.Dir, "checkout", "-q", "-b", "feature")
	write(t, r.Dir, "feature.txt", "f\n")
	commit(t, r.Dir, "feature")
	run(t, r.Dir, "checkout", "-q", "main")
	write(t, r.Dir, "main-only.txt", "m\n")
	commit(t, r.Dir, "main moves on")

	tree, err := r.MergeTree(ctx, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := r.Changes(ctx, "main", tree)
	if err != nil {
		t.Fatal(err)
	}
	// only the feature's own change: main's newer commit is in both sides
	if len(changes) != 1 || changes[0].Path != "feature.txt" {
		t.Fatalf("base vs merge result: %v", changes)
	}
	// comparing against the branch directly would show main's commit as a revert
	direct, _ := r.Changes(ctx, "main", "feature")
	if len(direct) != 2 {
		t.Fatalf("expected the stale comparison to show 2 paths, got %v", direct)
	}
}

func TestMergeTreeConflictNamesFiles(t *testing.T) {
	r := repo(t)
	ctx := context.Background()
	run(t, r.Dir, "checkout", "-q", "-b", "feature")
	write(t, r.Dir, "a.txt", "feature\n")
	commit(t, r.Dir, "feature")
	run(t, r.Dir, "checkout", "-q", "main")
	write(t, r.Dir, "a.txt", "main\n")
	commit(t, r.Dir, "main")

	_, err := r.MergeTree(ctx, "main", "feature")
	if err == nil || !strings.Contains(err.Error(), "conflicts in: a.txt") {
		t.Fatalf("want conflict error naming a.txt, got %v", err)
	}
}

func TestMergeTreeShallowCloneAsksForNoMerge(t *testing.T) {
	src := repo(t)
	ctx := context.Background()
	run(t, src.Dir, "checkout", "-q", "-b", "feature")
	write(t, src.Dir, "feature.txt", "f\n")
	commit(t, src.Dir, "feature")
	run(t, src.Dir, "checkout", "-q", "main")
	write(t, src.Dir, "main-only.txt", "m\n")
	commit(t, src.Dir, "main moves on")

	dir := t.TempDir()
	run(t, dir, "init", "-q")
	run(t, dir, "fetch", "-q", "--depth=1", "file://"+src.Dir, "main:main", "feature:feature")
	r := &Repo{Dir: dir}
	if !r.Shallow(ctx) {
		t.Fatal("expected a shallow clone")
	}
	if _, err := r.MergeTree(ctx, "main", "feature"); err == nil || !strings.Contains(err.Error(), "--no-merge") {
		t.Fatalf("want a --no-merge hint, got %v", err)
	}
	// a direct diff of two shallow commits still works
	if _, err := r.Changes(ctx, "main", "feature"); err != nil {
		t.Fatalf("diff of shallow commits: %v", err)
	}
}

func TestExportReplacesContent(t *testing.T) {
	r := repo(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "stale.txt", "from a previous side\n")
	if err := r.Export(ctx, "HEAD", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); !os.IsNotExist(err) {
		t.Fatal("export kept a stale file")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "a\n" {
		t.Fatalf("a.txt = %q", data)
	}
}

func TestFetchBaseRejectsLocalBranchNames(t *testing.T) {
	r := repo(t)
	if err := r.FetchBase(context.Background(), "main"); err == nil || !strings.Contains(err.Error(), "--offline") {
		t.Fatalf("want an --offline hint for a non-remote ref, got %v", err)
	}
}
