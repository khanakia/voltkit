// new_cmd.go — `volt new`: scaffold a project from the template repo
// (fetched at a pinned ref, never embedded — ADR-R13).
package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/khanakia/voltkit/apps/volt/forge"
	"github.com/khanakia/voltkit/apps/volt/scaffold"

	"github.com/spf13/cobra"
)

func newNewCommand() *cobra.Command {
	var (
		tmplVariant string
		tmplRepo    string
		ref         string
		module      string
		list        bool
	)
	cmd := &cobra.Command{
		Use:   "new cli <name>",
		Short: "Scaffold a project from the volt-cli templates",
		Long: `Scaffolds into ./<name> from the template repo, fetched at a pinned tag
(never a branch — a bad template commit must not break scaffolding).

  volt new cli mytool
  volt new cli mytool --template cobra --module github.com/you/mytool
  volt new cli --list

Only "cli" exists today; api and web arrive with their providers.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if args[0] != "cli" {
				return fmt.Errorf("kind %q: only \"cli\" has templates today", args[0])
			}
			root, commit, err := scaffold.Fetch(tmplRepo, ref)
			if err != nil {
				return err
			}
			meta, err := scaffold.LoadMeta(root)
			if err != nil {
				return err
			}
			if list {
				names := make([]string, 0, len(meta.Variants))
				for n := range meta.Variants {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					mark := " "
					if n == meta.Default {
						mark = "*"
					}
					_, _ = fmt.Fprintf(out, "%s %-10s %s\n", mark, n, meta.Variants[n].Description)
				}
				return nil
			}
			if len(args) != 2 {
				return fmt.Errorf("usage: volt new cli <name>")
			}
			name := args[1]
			if tmplVariant == "" {
				tmplVariant = meta.Default
			}
			if _, ok := meta.Variants[tmplVariant]; !ok {
				return fmt.Errorf("variant %q not in %s (run volt new cli --list)", tmplVariant, tmplRepo)
			}
			if module == "" {
				module = defaultModule(name)
				_, _ = fmt.Fprintf(out, "module defaulted to %s (override with --module)\n", module)
			}
			v := scaffold.Vars{
				Name: name, Module: module, Variant: tmplVariant,
				Ref: ref, TemplateRepo: tmplRepo, TemplateCommit: commit,
				VoltVersion: voltVersion, At: time.Now().UTC().Format(time.RFC3339),
			}
			files, err := scaffold.Generate(root, tmplVariant, name, v)
			if err != nil {
				return err
			}
			for _, f := range files {
				_, _ = fmt.Fprintf(out, "  %s\n", f)
			}
			_, _ = fmt.Fprintf(out, "\nscaffolded %s (%s@%s, variant %s)\nnext:\n  cd %s && git init && volt ci\n  volt release . --snapshot\n",
				name, tmplRepo, ref, tmplVariant, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&tmplVariant, "template", "", "variant to scaffold (default: the repo's declared default)")
	cmd.Flags().StringVar(&tmplRepo, "repo", scaffold.DefaultRepo, "template repository (owner/name)")
	cmd.Flags().StringVar(&ref, "ref", scaffold.DefaultRef, "template ref — a tag pinned per volt release")
	cmd.Flags().StringVar(&module, "module", "", "Go module path (default: github.com/<gh-user>/<name>)")
	cmd.Flags().BoolVar(&list, "list", false, "list available variants")
	return cmd
}

// defaultModule guesses github.com/<gh-user>/<name> from gh's login, falling
// back to a placeholder that fails loudly at `go mod tidy` rather than
// silently claiming a namespace.
func defaultModule(name string) string {
	// forge.GitHub constructed, not detected: there is no repo yet to detect
	// from — the module-path guess is a GitHub-account convention.
	login := (forge.GitHub{}).UserLogin()
	if login == "" {
		return "example.invalid/" + name
	}
	return "github.com/" + login + "/" + name
}
