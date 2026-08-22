// commands.go — the cobra surface: list, get, path, check, version, refresh.
// Every command starts from the same resolveDir (ensure→clean→serve), so
// serving can never observe a stale or half-built cache.
package skillcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// banner is the first line of every get output: the served content
// self-identifies, which is the agent's cheapest freshness verification —
// the binary stamps its own version at serve time, so it cannot be wrong.
func banner(o Options, source string) string {
	return fmt.Sprintf("<!-- %s %s · skills source: %s -->", o.Binary, o.Version, source)
}

func newListCommand(o Options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, o, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func runList(cmd *cobra.Command, o Options, asJSON bool) error {
	out := cmd.OutOrStdout()
	dir, _, err := resolveDir(o)
	if err != nil {
		return err
	}
	skills, err := LoadAll(dir)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(skills)
	}
	width := 0
	for _, s := range skills {
		if len(s.Name) > width {
			width = len(s.Name)
		}
	}
	for _, s := range skills {
		_, _ = fmt.Fprintf(out, "  %-*s  %s\n", width, s.Name, firstSentence(s.Description))
	}
	// The install hint is the binary's ONLY relationship to harness
	// installation (spec decision 10) — everything else is skills.sh's job.
	_, _ = fmt.Fprintf(out, "\ninstall for agents:  npx skills add %s\n", o.Repo)
	return nil
}

// firstSentence trims a description for the one-line list view.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func newGetCommand(o Options) *cobra.Command {
	var (
		full   bool
		all    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "get [name...]",
		Short: "Print a skill's full content",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dir, source, err := resolveDir(o)
			if err != nil {
				return err
			}
			skills, err := LoadAll(dir)
			if err != nil {
				return err
			}
			var selected []Skill
			switch {
			case all:
				selected = skills
			case len(args) == 0:
				return fmt.Errorf("name a skill, or use --all")
			default:
				for _, name := range args {
					sk, err := find(skills, name)
					if err != nil {
						return err
					}
					selected = append(selected, sk)
				}
			}
			if asJSON {
				return emitJSON(out, selected, full)
			}
			_, _ = fmt.Fprintln(out, banner(o, source))
			for _, sk := range selected {
				// The banner-per-skill delimiter lets an agent split an
				// --all stream unambiguously (spec, command surface).
				_, _ = fmt.Fprintf(out, "==> skill: %s <==\n", sk.Name)
				if err := emitSkill(out, sk, full); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "include every file under references/ and templates/")
	cmd.Flags().BoolVar(&all, "all", false, "every skill")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

// emitSkill prints one skill: the SKILL.md, then (with --full) each text
// supporting file behind a file banner. Binary files are LISTED, never
// inlined (spec S3): an agent fetches them via `skills path`.
func emitSkill(out io.Writer, sk Skill, full bool) error {
	raw, err := os.ReadFile(sk.SkillMD)
	if err != nil {
		return err
	}
	_, _ = out.Write(raw)
	if !full || sk.Dir == "" {
		return nil
	}
	for _, rel := range sk.Files {
		path := filepath.Join(sk.Dir, filepath.FromSlash(rel))
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isBinary(content) {
			_, _ = fmt.Fprintf(out, "--- file: %s (binary, %d bytes — use `skills path %s`) ---\n", rel, len(content), sk.Name)
			continue
		}
		_, _ = fmt.Fprintf(out, "--- file: %s ---\n", rel)
		_, _ = out.Write(content)
		if len(content) > 0 && content[len(content)-1] != '\n' {
			_, _ = fmt.Fprintln(out)
		}
	}
	return nil
}

// isBinary uses the standard NUL-byte heuristic over the first KB.
func isBinary(b []byte) bool {
	probe := b
	if len(probe) > 1024 {
		probe = probe[:1024]
	}
	for _, c := range probe {
		if c == 0 {
			return true
		}
	}
	return false
}

// jsonSkill is the --json shape for get.
type jsonSkill struct {
	Skill
	Content string `json:"content"`
}

func emitJSON(out io.Writer, skills []Skill, full bool) error {
	var payload []jsonSkill
	for _, sk := range skills {
		var b strings.Builder
		if err := emitSkill(&b, sk, full); err != nil {
			return err
		}
		payload = append(payload, jsonSkill{Skill: sk, Content: b.String()})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func newPathCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "path [name]",
		Short: "Print the filesystem path of the skills (or one skill)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _, err := resolveDir(o)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), dir)
				return nil
			}
			skills, err := LoadAll(dir)
			if err != nil {
				return err
			}
			sk, err := find(skills, args[0])
			if err != nil {
				return err
			}
			if sk.Dir == "" { // single-file sugar: the path IS the md file
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), sk.SkillMD)
				return nil
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), sk.Dir)
			return nil
		},
	}
}

func newCheckCommand(o Options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check <installed-dir>",
		Short: "Is an installed copy of a skill current for this binary?",
		Long: `Compares an installed skill directory (wherever your agent harness keeps
it) against this binary's copy of the same skill. The verdict is decided
only by this binary's files — junk like .DS_Store in the installed copy is
ignored. Nothing is ever written.

The skill is identified by the installed SKILL.md's name (frontmatter),
falling back to the directory's name.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			installed := args[0]

			// Identify WHICH skill this directory claims to be: the
			// installed SKILL.md's frontmatter name wins (a harness may
			// rename the directory); an unreadable/absent SKILL.md falls
			// back to the directory name — the comparison itself will then
			// surface the missing file as STALE.
			name := filepath.Base(installed)
			if sk, err := loadDirSkill(installed); err == nil {
				name = sk.Name
			}

			dir, _, err := resolveDir(o)
			if err != nil {
				return err
			}
			skills, err := LoadAll(dir)
			if err != nil {
				return err
			}
			ref, err := find(skills, name)
			if err != nil {
				return err
			}
			if ref.Dir == "" {
				return fmt.Errorf("%s is a single-file skill — compare against `skills get %s` directly", name, name)
			}
			res, err := CompareDirs(ref.Dir, installed)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			for _, extra := range res.Extras {
				_, _ = fmt.Fprintf(out, "note: %s is not part of this skill (ignored)\n", extra)
			}
			if res.Current {
				_, _ = fmt.Fprintf(out, "%s  current\n", name)
				return nil
			}
			_, _ = fmt.Fprintf(out, "%s  STALE — differs from this binary's skills: %s\n", name, strings.Join(res.Stale, ", "))
			_, _ = fmt.Fprintf(out, "refresh: npx skills add %s   (or follow `%s skills get %s` instead)\n", o.Repo, o.Binary, name)
			// Non-zero so scripts and agents cannot mistake stale for fine.
			return fmt.Errorf("stale")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func newVersionCommand(o Options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Binary version, canonical skills hash, and the serving source",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, source, err := resolveDir(o)
			if err != nil {
				return err
			}
			hash, err := TreeHash(dir)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{
					"version": o.Version, "skills_hash": hash, "source": source, "path": dir,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\nskills_hash: %s\nsource: %s (%s)\n", o.Binary, o.Version, hash, source, dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func newRefreshCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Re-download this version's skills bundle (publish-recovery; never automatic)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(o.Env) != "" || IsDevVersion(o.Version) {
				return fmt.Errorf("refresh only applies to the version cache — this invocation serves from a live directory")
			}
			dir, err := refresh(o)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "re-fetched %s skills into %s\n", o.Version, dir)
			return nil
		},
	}
}
