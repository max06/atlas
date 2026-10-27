package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/max06/atlas/internal/templatetest"
)

func newTestCmd(g *globalFlags) *cobra.Command {
	var decrypt bool
	cmd := &cobra.Command{
		Use:   "test [template...]",
		Short: "Run the test suites of templates (templates/<name>/tests/*_test.yaml, experimental)",
		Long: `Runs the test suites of the tree's templates. A test renders the template
through ATLAS in the context of a deployment that instantiates it (the suite's
cluster/deployment, else the first one), with the test's values added to that
instance, and checks the manifests with helm-unittest assertions:

  suite: kgateway
  cluster: alpha            # optional
  deployment: kgateway      # optional
  tests:
    - it: runs two replicas
      values:
        - replicas: 2
      release: kgateway     # optional: only this release
      asserts:
        - isKind: {of: Deployment}
          template: templates/deployment.yaml
        - equal: {path: spec.replicas, value: 2}
          template: templates/deployment.yaml

An assert's template is the path in the chart, the path in the release
(<release>/<chart>/...) or a glob of either; without it, every file of the
deployment. Keyless unless --decrypt.`,
		RunE: func(cmd *cobra.Command, templates []string) error {
			work, err := os.MkdirTemp("", "atlas-test-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)
			results, err := templatetest.Run(cmd.Context(), templatetest.Options{
				Dir: g.dir, Entry: g.file, Helmfile: g.helmfile, Templates: templates,
				Decrypt: decrypt, Work: filepath.Join(work, "w"), Log: os.Stderr,
			})
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no template tests found")
				return nil
			}
			failed := 0
			out := cmd.OutOrStdout()
			for _, r := range results {
				status := "PASS"
				if !r.Passed {
					status = "FAIL"
					failed++
				}
				fmt.Fprintf(out, "%s  %s: %s  (%s, %s)\n", status, r.Suite, r.It, strings.Replace(r.Pair, "|", "/", 1), r.File)
				for _, f := range r.Failures {
					fmt.Fprintf(out, "      %s\n", f)
				}
			}
			fmt.Fprintf(out, "\n%d test(s), %d failed\n", len(results), failed)
			if failed > 0 {
				return fmt.Errorf("%d template test(s) failed", failed)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&decrypt, "decrypt", false, "decrypt SOPS files (needs the key)")
	return cmd
}
