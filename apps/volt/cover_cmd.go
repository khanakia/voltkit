// cover_cmd.go — `volt cover`: test coverage for any repo, and the README
// badge (a committed SVG — no Codecov, no shields.io, works private).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/khanakia/voltkit/apps/volt/covercheck"

	"github.com/spf13/cobra"
)

func newCoverCommand() *cobra.Command {
	var (
		asJSON     bool
		badge      string
		badgeCheck bool
		min        float64
	)
	cmd := &cobra.Command{
		Use:   "cover [dir]",
		Short: "Measure test coverage — per module, one honest total, optional README badge",
		Long: `Runs go test with coverage in every module under [dir] (default: the whole
repo), merges the profiles, and reports a statement-weighted total (never an
average of module percentages).

  volt cover                          every module, table + total
  volt cover ./saas                   just the modules under one directory
  volt cover --json                   {"total":..,"modules":[..]}
  volt cover --badge coverage.svg     also write a self-contained SVG badge
  volt cover --min 70                 exit non-zero below the floor

Badge usage in a README:  ![coverage](./coverage.svg)
Regenerate it in CI (volt cover --badge coverage.svg) or before committing.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			// Progress streams to stderr so --json output on stdout stays
			// machine-clean even while a slow suite runs.
			rep, err := covercheck.Run(root, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			} else {
				for _, m := range rep.Modules {
					_, _ = fmt.Fprintf(out, "%6.1f%%  %s\n", m.Percent, m.Module)
				}
				_, _ = fmt.Fprintf(out, "%6.1f%%  total (statement-weighted)\n", rep.Total)
			}
			if badge != "" {
				rendered := []byte(covercheck.BadgeSVG(rep.Total))
				if badgeCheck {
					// --check: verify the committed badge matches reality —
					// a stale badge is a small lie in the README. Exit
					// non-zero with the fix rather than silently rewriting.
					existing, err := os.ReadFile(badge)
					if err != nil || string(existing) != string(rendered) {
						return fmt.Errorf("%s is stale (coverage is %.1f%%) — regenerate with `volt cover --badge %s` and commit it", badge, rep.Total, badge)
					}
					_, _ = fmt.Fprintf(out, "badge %s is current\n", badge)
				} else {
					if err := os.WriteFile(badge, rendered, 0o644); err != nil {
						return err
					}
					_, _ = fmt.Fprintf(out, "badge written to %s\n", badge)
				}
			}
			if min > 0 && rep.Total < min {
				return fmt.Errorf("coverage %.1f%% is below the --min floor of %.1f%%", rep.Total, min)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.Flags().StringVar(&badge, "badge", "", "write a self-contained SVG badge to this path")
	cmd.Flags().Float64Var(&min, "min", 0, "fail when total coverage is below this percentage")
	cmd.Flags().BoolVar(&badgeCheck, "check", false, "with --badge: fail if the committed badge is stale instead of rewriting it")
	return cmd
}
