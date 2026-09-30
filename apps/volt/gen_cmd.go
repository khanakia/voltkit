// gen_cmd.go — `volt gen`: write the generated files (workflow stubs +
// install scripts) with hash-guarded headers (ADR-R11).
package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/detect"
	"github.com/khanakia/voltkit/apps/volt/forge"
	"github.com/khanakia/voltkit/apps/volt/genfiles"
	"github.com/khanakia/voltkit/apps/volt/relname"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"

	"github.com/spf13/cobra"
)

// voltVersion/voltCommit are stamped by the release pipeline (see .volt.yml
// ldflags.vars); "dev" from source builds. Recorded in generated-file headers
// so a file names the volt that wrote it.
var (
	voltVersion = "dev"
	voltCommit  = "unknown"
)

func newGenCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "gen [skills | install <dir>]",
		Short: "Write generated files: workflow stubs, install scripts — or the skills wiring",
		Long: `Every file carries a "DO NOT EDIT" header with a body hash. Regeneration
refuses to overwrite a hand-edited file (and prints the diff); --force
overwrites. Files volt never touches: CHANGELOG.md, README.md, .volt.yml.

  volt gen                the standard set: workflow stubs + install scripts
  volt gen skills         the skillcmd wiring file, plus a one-time starter skill
                          when skills/ does not exist yet (SKILLCMD_SPEC)
  volt gen install <dir>  install.sh + install.ps1 at the repo root for the CLI
                          in <dir> — for a repo whose root is not a single CLI
                          (several binaries, or a library with a CLI beside it);
                          the scripts resolve that CLI's own tag stream`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := voltcfg.Load(".")
			if err != nil {
				return err
			}
			f, err := detectForge(".")
			if err != nil {
				return err
			}
			repo := f.RepoOf(".").String()
			if repo == "" {
				return fmt.Errorf("cannot derive the repo (owner/name) — run inside a repo with an origin remote and forge auth configured (GitHub: gh authenticated)")
			}
			if len(args) == 1 && args[0] == "skills" {
				return genSkills(cmd, cfg, f, repo, force)
			}
			if len(args) >= 1 && args[0] == "install" {
				if len(args) != 2 {
					return fmt.Errorf("volt gen install needs the CLI's directory, e.g. volt gen install ./cmd/notes")
				}
				return genInstall(cmd, f, repo, args[1], force)
			}
			if len(args) >= 1 {
				return fmt.Errorf(`unknown gen target %q — "skills" and "install <dir>" exist`, args[0])
			}
			// Root kind decides whether the install scripts apply: they
			// install THE binary of a single-CLI repo. A library root or a
			// no-package root (monorepo) skips them — loudly, not silently.
			rootIsCLI := false
			if k, err := detect.Dir("."); err == nil && k == detect.KindCLI {
				rootIsCLI = true
			}
			v := forgeVars(f, forge.Repo(repo), cfg.Binary)
			v.ExtraBinaries = extraBinaryNames(".", cfg)
			refused, err := writeGenerated(cmd, append(append([]genfiles.File{}, f.CIFiles()...), genfiles.InstallScripts...), v, rootIsCLI, force)
			if err != nil {
				return err
			}
			if refused > 0 {
				return fmt.Errorf("%d file(s) refused — review the diffs above", refused)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite hand-edited generated files")
	return cmd
}

// writeGenerated renders files through the hash guard and reports each
// outcome; CLIOnly files are skipped, loudly, unless includeCLIOnly.
// It returns how many files were refused as hand-edited.
func writeGenerated(cmd *cobra.Command, files []genfiles.File, v genfiles.Vars, includeCLIOnly, force bool) (int, error) {
	out := cmd.OutOrStdout()
	refused := 0
	for _, gf := range files {
		if gf.CLIOnly && !includeCLIOnly {
			_, _ = fmt.Fprintf(out, "skipped %s — repo root is not a single CLI (install scripts need one binary to install); for a CLI in a subdirectory: volt gen install <dir>\n", gf.RelPath)
			continue
		}
		res, err := genfiles.Generate(".", gf, v, force)
		if err != nil {
			return refused, err
		}
		switch res.Outcome {
		case genfiles.Written:
			_, _ = fmt.Fprintf(out, "wrote %s\n", gf.RelPath)
		case genfiles.Unchanged:
			_, _ = fmt.Fprintf(out, "unchanged %s\n", gf.RelPath)
		case genfiles.Refused:
			refused++
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "REFUSED %s — hand-edited; --force to overwrite\n%s", gf.RelPath, res.Diff)
		}
	}
	return refused, nil
}

// extraBinaryNames resolves .volt.yml's extra_binaries, given relative to the
// released directory, to the names the build gives them: each directory's own
// base name, exactly as gobuild names the archive entries.
func extraBinaryNames(dir string, cfg voltcfg.Config) []string {
	var names []string
	for _, rel := range cfg.ExtraBinaries {
		abs, err := filepath.Abs(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		names = append(names, filepath.Base(abs))
	}
	return names
}

// genInstall implements `volt gen install <dir>`: the install scripts for the
// CLI in dir, written at the repo root. The scripts carry that CLI's tag
// prefix (rule one, via relname.Compose — the same composer release uses), so
// they find its newest release in its own stream rather than trusting the
// repo-global "latest", which in a multi-stream repo is whichever stream
// released last.
func genInstall(cmd *cobra.Command, f forge.Forge, repo, dir string, force bool) error {
	kind, err := detect.Dir(dir)
	if err != nil {
		return err
	}
	if kind != detect.KindCLI {
		return fmt.Errorf("%s is not a CLI (no package main): install scripts install a binary", dir)
	}
	cfg, err := voltcfg.Load(dir)
	if err != nil {
		return err
	}
	rel, err := repoRel(dir)
	if err != nil {
		return err
	}
	prefix, err := tagPrefixFor(kind, rel, cfg.Binary)
	if err != nil {
		return err
	}
	v := forgeVars(f, forge.Repo(repo), cfg.Binary)
	v.TagPrefix = prefix
	v.ExtraBinaries = extraBinaryNames(dir, cfg)
	v.Regenerate = "volt gen install " + filepath.ToSlash(rel)
	refused, err := writeGenerated(cmd, genfiles.InstallScripts, v, true, force)
	if err != nil {
		return err
	}
	if refused > 0 {
		return fmt.Errorf("%d file(s) refused — review the diffs above", refused)
	}
	return nil
}

// genSkills implements `volt gen skills`: the guarded wiring file, the
// one-time starter skill, and the module dependency (SKILLCMD_SPEC).
func genSkills(cmd *cobra.Command, cfg voltcfg.Config, f forge.Forge, repo string, force bool) error {
	out := cmd.OutOrStdout()
	if cfg.Skills.ManagedDisabled() {
		return fmt.Errorf("skills.managed is false in %s — volt must leave this skills directory alone; remove the setting if that changed", voltcfg.FileName)
	}
	// The tag shape mirrors rule one: a CLI's bundles live under its
	// binary-name stream (tag "notes/"+version), a root release under the
	// bare version. The wiring records the PREFIX; skillcmd appends the
	// version at runtime (empty prefix ⇒ Tag defaults to Version there).
	//
	// Derived through relname.Compose — the SAME composer release uses —
	// so the wiring can never disagree with the tag release will create.
	// (A root single-CLI repo tags bare; hardcoding binary+"/" here once
	// generated a fetch tag that no release would ever produce.)
	kind, err := detect.Dir(".")
	if err != nil {
		return err
	}
	prefix, err := tagPrefix(kind, cfg.Binary)
	if err != nil {
		return err
	}
	sv := genfiles.SkillsVars{
		Binary: cfg.Binary, Repo: repo, Tag: prefix, Version: voltVersion,
		AssetTpl: f.AssetURL(forge.Repo(repo), "{tag}", "{asset}"),
	}

	if created, err := genfiles.StarterSkill(".", sv); err != nil {
		return err
	} else if created != "" {
		_, _ = fmt.Fprintf(out, "created starter skill %s — REPLACE its placeholder content\n", created)
	}
	res, err := genfiles.GenerateSkillsWiring(".", sv, force)
	if err != nil {
		return err
	}
	switch res.Outcome {
	case genfiles.Written:
		_, _ = fmt.Fprintf(out, "wrote %s\n", genfiles.SkillsWiring.RelPath)
	case genfiles.Unchanged:
		_, _ = fmt.Fprintf(out, "unchanged %s\n", genfiles.SkillsWiring.RelPath)
	case genfiles.Refused:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "REFUSED %s — hand-edited; --force to overwrite\n%s", genfiles.SkillsWiring.RelPath, res.Diff)
		return fmt.Errorf("refused — review the diff above")
	}
	_ = runQuiet("go", "get", "github.com/khanakia/voltkit/skillcmd")
	_, _ = fmt.Fprintf(out, `
wire it in (one of):
  cobra app:      root.AddCommand(newSkillsCommand())
  flag-based app: if len(os.Args) > 1 && os.Args[1] == "skills" {
                      cmd := newSkillsCommand(); cmd.SetArgs(os.Args[2:])
                      if err := cmd.Execute(); err != nil { os.Exit(1) }
                      return
                  }
`)
	return nil
}

// tagPrefix computes the release-tag prefix for the CURRENT directory by
// asking relname.Compose — the one authority on tag shapes — with a probe
// version, then stripping it. cwd at the repo root ⇒ "" (bare tags);
// a CLI subdir ⇒ "<binary>/"; a library subdir ⇒ "<path>/".
func tagPrefix(kind detect.Kind, binary string) (string, error) {
	rel, err := repoRel(".")
	if err != nil {
		return "", err
	}
	return tagPrefixFor(kind, rel, binary)
}

// repoRel returns dir relative to the repository's top level.
func repoRel(dir string) (string, error) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(top))
	// Resolve symlinks on both sides: git reports the physical top level, and
	// a path through a symlinked parent would otherwise leave the tree.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if a, err := filepath.EvalSymlinks(abs); err == nil {
		abs = a
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the repository", dir)
	}
	return rel, nil
}

// tagPrefixFor is tagPrefix for a directory given relative to the repo root.
func tagPrefixFor(kind detect.Kind, rel, binary string) (string, error) {
	const probe = "v0.0.0" // any valid version; only the prefix survives
	tag, err := relname.Compose(kind, rel, binary, probe)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(tag, probe), nil
}

// runQuiet runs a command discarding output; used for best-effort steps
// whose failure the next build reports better than we can here.
func runQuiet(name string, args ...string) error {
	c := exec.Command(name, args...)
	return c.Run()
}
