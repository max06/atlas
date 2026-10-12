package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/ci"
	"github.com/max06/atlas/internal/gha"
)

// newReviewCICmd groups the review pipeline steps the GitHub actions run.
// Each step keeps the environment contract of the bash script it replaced
// (named in its help), so action entrypoints and workflow tests work with
// either. `action render` / `action diff` are the actions themselves (INPUT_*
// variables, as GitHub sets them for a docker action).
func newReviewCICmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "ci",
		Short:  "Review pipeline steps for CI (environment contract of the former scripts)",
		Hidden: true,
		// the steps read ATLAS_SIDEDUMP_MAP_DIR from the environment on
		// purpose (render.sh contract): no "ignoring ATLAS_*" warning
		PersistentPreRun: func(*cobra.Command, []string) {},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "discover <output.json>",
			Short: "Write the discovery map of $HELMFILE_PATH (discover.sh): exit 2 = no map, 1 = build failed",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				entry := os.Getenv("HELMFILE_PATH")
				if entry == "" {
					return errors.New("HELMFILE_PATH is required")
				}
				return ci.Discover(cmd.Context(), entry, g.helmfile, args[0], os.Stdout)
			},
		},
		&cobra.Command{
			Use:   "render",
			Short: "Render one side of a review (render.sh environment)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, err := ci.RenderConfigFromEnv()
				if err != nil {
					return err
				}
				c.Helmfile = g.helmfile
				return ci.Render(cmd.Context(), c)
			},
		},
		&cobra.Command{
			Use:   "scrub <baseline-root> <map-dir>",
			Short: "Replay redaction maps over a baseline render tree (scrub-baseline.sh)",
			Args:  cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error {
				_, err := ci.Scrub(args[0], args[1], os.Stdout, os.Stderr)
				return err
			},
		},
		&cobra.Command{
			Use:   "replay",
			Short: "Scrub $BASELINE_DIR with $SIDEDUMP_MAP_DIR, write replay-status.txt (replay.sh)",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				_, err := ci.Replay(ci.ReplayConfig{
					BaselineDir: os.Getenv("BASELINE_DIR"),
					SidedumpDir: os.Getenv("SIDEDUMP_MAP_DIR"),
					Temp:        tempDir("REPLAY_TEMP"),
					Outputs:     gha.FromEnv(),
					Log:         os.Stdout,
				})
				return err
			},
		},
		&cobra.Command{
			Use:   "diff",
			Short: "Diff $BASELINE_DIR and $PR_DIR per resource (diff.sh)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				temp := tempDir("DIFF_TEMP")
				_, err := ci.Diff(cmd.Context(), ci.DiffConfig{
					BaselineDir:      os.Getenv("BASELINE_DIR"),
					PRDir:            os.Getenv("PR_DIR"),
					Temp:             temp,
					Mode:             envOr("DIFF_MODE", "dyff"),
					SidedumpDir:      os.Getenv("SIDEDUMP_MAP_DIR"),
					ReplayStatusFile: os.Getenv("REPLAY_STATUS_FILE"),
					Outputs:          gha.FromEnv(),
				})
				return err
			},
		},
		&cobra.Command{
			Use:   "shadow",
			Short: "Check $AFFECTED_PATHS_FILE against $CLASSIFY_JSON (shadow-check.sh)",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				affected, classifyJSON := os.Getenv("AFFECTED_PATHS_FILE"), os.Getenv("CLASSIFY_JSON")
				if affected == "" || classifyJSON == "" {
					return errors.New("AFFECTED_PATHS_FILE and CLASSIFY_JSON are required")
				}
				out := os.Getenv("SHADOW_OUT")
				if out == "" {
					out = dirOf(classifyJSON)
				}
				_, err := ci.Shadow(affected, classifyJSON, out, gha.FromEnv(), os.Stdout)
				return err
			},
		},
		&cobra.Command{
			Use:   "comment",
			Short: "Build the PR comment and job summary (comment.sh environment)",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				temp := tempDir("COMMENT_TEMP")
				in, err := ci.CommentFromEnv(temp)
				if err != nil {
					return err
				}
				return ci.WriteComment(in, temp, gha.FromEnv())
			},
		},
		newReviewActionCmd(g),
	)
	return cmd
}

func newReviewActionCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "action",
		Short: "The review's GitHub actions (INPUT_* environment)",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "render",
			Short: "atlas-render: discover, classify or render one side",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return ci.RenderAction(cmd.Context(), g.helmfile, os.Stdout)
			},
		},
		&cobra.Command{
			Use:   "diff",
			Short: "atlas-diff: replay, diff, scope/shadow check, comment",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return ci.DiffAction(cmd.Context(), os.Stdout)
			},
		},
	)
	return cmd
}

// tempDir is $<name>, else $RUNNER_TEMP, else /tmp.
func tempDir(name string) string {
	for _, v := range []string{os.Getenv(name), os.Getenv("RUNNER_TEMP")} {
		if v != "" {
			return v
		}
	}
	return "/tmp"
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

// exitCode maps a step's ExitError to its exit code (printing its message).
func exitCode(err error) (int, bool) {
	var e *ci.ExitError
	if errors.As(err, &e) {
		fmt.Fprintln(os.Stderr, e.Msg)
		return e.Code, true
	}
	return 0, false
}
