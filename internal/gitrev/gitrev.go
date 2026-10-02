// Package gitrev resolves the two revisions a review compares and
// materializes them as plain directory trees.
//
// The review question is "what changes if this merges now": the baseline is
// the CURRENT tip of the target branch, the other side is the merge result.
// Comparing against the merge-base (`main...HEAD`) would hide everything that
// landed on the target since the branch point, and comparing against a stale
// local `main` shows those commits as reverts. So:
//
//   - locally the base is a remote-tracking ref (origin/<default>) after a
//     fetch of just that ref, and the merge result is built with
//     `git merge-tree --write-tree` — no checkout, no index change;
//   - in CI the platform already made the merge commit (a shallow clone has no
//     history to merge in anyway), so both commits are passed explicitly and
//     nothing is merged.
//
// Uncommitted work is reviewable: Snapshot turns the working tree (tracked
// changes and untracked, non-ignored files) into a commit object through a
// throwaway index, leaving the real index and `git status` untouched.
package gitrev

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a git working tree.
type Repo struct {
	Dir string
}

// Open finds the repository containing dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	top, err := r.git(ctx, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	r.Dir = top
	return r, nil
}

func (r *Repo) git(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		// stdout is returned too: some commands (merge-tree) report on stdout when failing
		return strings.TrimSpace(stdout.String()), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Resolve returns the full commit id of rev.
func (r *Repo) Resolve(ctx context.Context, rev string) (string, error) {
	return r.git(ctx, nil, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// Shallow reports whether the clone is shallow (CI checkouts usually are).
func (r *Repo) Shallow(ctx context.Context) bool {
	out, err := r.git(ctx, nil, "rev-parse", "--is-shallow-repository")
	return err == nil && out == "true"
}

// DefaultBase picks the remote-tracking ref a local review compares against:
// origin/HEAD when it points somewhere, else origin/main. origin/HEAD is not
// trusted blindly — a clone made from a local path inherits that clone's
// checked-out branch — so callers print what was picked.
func (r *Repo) DefaultBase(ctx context.Context, remote string) string {
	if out, err := r.git(ctx, nil, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil && out != "" {
		return out
	}
	return remote + "/main"
}

// FetchBase refreshes a remote-tracking ref ("origin/main") by fetching only
// that branch. Other refs are left alone.
func (r *Repo) FetchBase(ctx context.Context, base string) error {
	remote, branch, ok := strings.Cut(base, "/")
	if !ok {
		return fmt.Errorf("base %q is not a remote-tracking ref (<remote>/<branch>); pass --offline to use it as is", base)
	}
	if _, err := r.git(ctx, nil, "remote", "get-url", remote); err != nil {
		return fmt.Errorf("base %q: no remote %q (pass --offline to use the ref as is)", base, remote)
	}
	_, err := r.git(ctx, nil, "fetch", "--quiet", remote,
		fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", branch, remote, branch))
	return err
}

// Snapshot returns a commit whose tree is the current working tree: tracked
// files with their uncommitted changes plus untracked files that .gitignore
// does not exclude. HEAD is its parent, so merge-tree finds the right base.
// The real index is never touched (a copy of it is staged into instead).
func (r *Repo) Snapshot(ctx context.Context) (string, error) {
	gitDir, err := r.git(ctx, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "atlas-index-*")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	// Start from the real index (fast: unchanged files keep their stat data).
	if data, err := os.ReadFile(filepath.Join(gitDir, "index")); err == nil {
		if err := os.WriteFile(tmp.Name(), data, 0o600); err != nil {
			return "", err
		}
	} else {
		os.Remove(tmp.Name()) // no index yet: let git create a fresh one
	}
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	if _, err := r.git(ctx, env, "add", "--all", "--", "."); err != nil {
		return "", err
	}
	tree, err := r.git(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree, "-m", "atlas review: working tree snapshot"}
	if head, err := r.Resolve(ctx, "HEAD"); err == nil {
		args = append(args, "-p", head)
	}
	return r.git(ctx, []string{
		"GIT_AUTHOR_NAME=atlas", "GIT_AUTHOR_EMAIL=atlas@localhost",
		"GIT_COMMITTER_NAME=atlas", "GIT_COMMITTER_EMAIL=atlas@localhost",
	}, args...)
}

// MergeTree returns the tree id of merging head into base, without touching
// the working tree or index. A conflict is an error: there is no single
// "after merge" state to review.
func (r *Repo) MergeTree(ctx context.Context, base, head string) (string, error) {
	out, err := r.git(ctx, nil, "merge-tree", "--write-tree", "--name-only", "--no-messages", base, head)
	if err != nil {
		if strings.Contains(err.Error(), "unrelated histories") {
			return "", fmt.Errorf("cannot merge %s into %s: no common history (shallow clone?) — pass both commits with --no-merge", head, base)
		}
		// exit status 1 = conflicts; stdout lists the tree id, then the conflicted files
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) > 1 {
			return "", fmt.Errorf("merging %s into %s conflicts in: %s", head, base, strings.Join(lines[1:], ", "))
		}
		return "", err
	}
	return strings.SplitN(out, "\n", 2)[0], nil
}

// Change is one line of `git diff --name-status --no-renames`.
type Change struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// Changes lists the paths that differ between two tree-ish revisions. Renames
// are split into a delete and an add, so every path is reported on its own.
func (r *Repo) Changes(ctx context.Context, from, to string) ([]Change, error) {
	out, err := r.git(ctx, nil, "diff", "--name-status", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	return ParseNameStatus(out), nil
}

// ParseNameStatus parses `git diff --name-status --no-renames` output.
func ParseNameStatus(out string) []Change {
	var changes []Change
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		changes = append(changes, Change{Status: fields[0], Path: fields[1]})
	}
	return changes
}

// Export replaces dir's content with the files of a tree-ish revision.
// The same dir is reused for both sides of a review: rendered values embed the
// absolute checkout path (atlas.cwd), so two sides rendered from different
// paths would differ everywhere.
func (r *Repo) Export(ctx context.Context, rev, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "atlas-export-index-*")
	if err != nil {
		return err
	}
	tmp.Close()
	os.Remove(tmp.Name())
	defer os.Remove(tmp.Name())
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	if _, err := r.git(ctx, env, "read-tree", rev); err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	_, err = r.git(ctx, env, "checkout-index", "--all", "--prefix="+abs+"/")
	return err
}
