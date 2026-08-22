// upgrade_cmd.go — `volt upgrade`: re-apply template evolution to a
// scaffolded project (spec D13). Fetching happens here; the merge engine in
// the upgrade package is purely local.
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/khanakia/voltkit/apps/volt/gitx"
	"github.com/khanakia/voltkit/apps/volt/scaffold"
	"github.com/khanakia/voltkit/apps/volt/upgrade"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"

	"github.com/spf13/cobra"
)

func newUpgradeCommand() *cobra.Command {
	var toRef string
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Re-apply template changes to this scaffolded project (three-way, edits survive)",
		Long: `Re-renders the template this project was scaffolded from — at its RECORDED
commit, with the RECORDED inputs — renders the target ref the same way, and
applies the difference three-way: your edits survive, overlaps become
git-style conflict markers to resolve by hand.

Requires a clean working tree, so the whole upgrade is reviewable with
git diff and revertible with git checkout.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cfg, err := voltcfg.Load(".")
			if err != nil {
				return err
			}
			st, err := upgrade.ParseStamp(cfg.Generated)
			if err != nil {
				return err
			}
			repo, err := st.Repo()
			if err != nil {
				return err
			}

			// Clean tree required: the upgrade's whole safety story is
			// "review with git diff, revert with git checkout" — impossible
			// when the diff would mix upgrade changes with pending work.
			dirty, err := gitx.IsDirty(".")
			if err == nil && dirty {
				return fmt.Errorf("working tree is dirty — commit or stash first, so the upgrade is reviewable as its own diff")
			}

			// Old = the RECORDED commit (a moved tag must not change what
			// "old" means); new = the target ref resolved now.
			oldRoot, _, err := scaffold.Fetch(repo, st.Commit)
			if err != nil {
				return fmt.Errorf("fetching the original template: %w", err)
			}
			newRoot, newCommit, err := scaffold.Fetch(repo, toRef)
			if err != nil {
				return fmt.Errorf("fetching the target template: %w", err)
			}
			if newCommit == st.Commit {
				_, _ = fmt.Fprintf(out, "already at %s (%s) — nothing to upgrade\n", toRef, shortSHA(newCommit))
				return nil
			}

			oldVars := scaffold.Vars{
				Name: st.Name, Module: st.Module, Variant: st.Variant,
				Ref: st.Ref, TemplateRepo: repo, TemplateCommit: st.Commit,
				VoltVersion: strings.TrimPrefix(st.By, "volt "), At: st.At,
			}
			newVars := scaffold.Vars{
				Name: st.Name, Module: st.Module, Variant: st.Variant,
				Ref: toRef, TemplateRepo: repo, TemplateCommit: newCommit,
				VoltVersion: voltVersion, At: time.Now().UTC().Format(time.RFC3339),
			}

			rep, err := upgrade.Run(".", oldRoot, newRoot, st.Variant, oldVars, newVars, out)
			if err != nil {
				return err
			}
			if len(rep.Results) == 0 {
				_, _ = fmt.Fprintf(out, "template content identical for your inputs — nothing to change\n")
				return nil
			}
			if rep.Conflicts > 0 {
				return fmt.Errorf("%d file(s) have conflict markers — resolve them, then commit (search for <<<<<<<)", rep.Conflicts)
			}
			_, _ = fmt.Fprintf(out, "upgraded to %s (%s) — review with git diff, then commit\n", toRef, shortSHA(newCommit))
			return nil
		},
	}
	cmd.Flags().StringVar(&toRef, "ref", scaffold.DefaultRef, "target template ref")
	return cmd
}

// shortSHA trims a commit for display.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
