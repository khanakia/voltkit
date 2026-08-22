// ci_cmd.go — `volt ci`: the same gate locally and in CI (spec, build-order
// step 3). Default scope is modules changed vs origin's default branch;
// --all runs everything (what main should run).
package main

import (
	"fmt"
	"os"

	"github.com/khanakia/voltkit/apps/volt/cicheck"
	"github.com/khanakia/voltkit/apps/volt/genfiles"
	"github.com/khanakia/voltkit/apps/volt/gitx"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"

	"github.com/spf13/cobra"
)

func newCICommand() *cobra.Command {
	var (
		all bool
		fix bool
	)
	cmd := &cobra.Command{
		Use:   "ci [dir]",
		Short: "fmt-check, vet, lint and race-test — changed modules, one dir, or --all",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			var mods []string
			var err error
			switch {
			case len(args) == 1:
				mods = []string{args[0]}
			case all:
				mods, err = cicheck.Modules(".")
			default:
				// merge-base with origin/HEAD when it exists; "" (no remote,
				// shallow clone) safely widens to everything.
				base := gitx.MergeBase(".", "origin/HEAD")
				mods, err = cicheck.Changed(".", base)
			}
			if err != nil {
				return err
			}
			if len(mods) == 0 {
				return fmt.Errorf("no Go modules found under this directory")
			}
			var problems []string
			for _, m := range mods {
				problems = append(problems, cicheck.Gate(".", m, out, fix)...)
			}
			problems = append(problems, lintSkillsIfPresent(cmd)...)
			if len(problems) > 0 {
				for _, p := range problems {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "FAIL: %s\n", p)
				}
				return fmt.Errorf("%d problem(s) across %d module(s)", len(problems), len(mods))
			}
			_, _ = fmt.Fprintf(out, "ok — %d module(s) clean\n", len(mods))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "run every module, not just changed ones")
	cmd.Flags().BoolVar(&fix, "fix", false, "apply gofmt and lint auto-fixes first; report only what remains")
	return cmd
}

// lintSkillsIfPresent runs the skills lint per SKILLCMD_SPEC: absent dir →
// nothing; `skills.managed: false` → a loud skip; present and managed →
// frontmatter contract enforced, prefix convention warned.
func lintSkillsIfPresent(cmd *cobra.Command) []string {
	cfg, err := voltcfg.Load(".")
	if err != nil {
		return []string{err.Error()}
	}
	dir := cfg.Skills.SkillsDir()
	if _, err := os.Stat(dir); err != nil {
		return nil // no skills directory — the check does not exist
	}
	if cfg.Skills.ManagedDisabled() {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "notice: %s/ present but skills.managed is false — lint skipped\n", dir)
		return nil
	}
	problems, warnings, err := genfiles.LintSkills(dir, cfg.Binary)
	if err != nil {
		return []string{fmt.Sprintf("skills lint: %v", err)}
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warn: %s\n", w)
	}
	return problems
}
