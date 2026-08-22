package genfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var v = Vars{Repo: "khanakia/notes", Binary: "notes", Version: "v0.1.0"}

func gen(t *testing.T, root string, f File, force bool) Result {
	t.Helper()
	res, err := Generate(root, f, v, force)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// allFiles is every generated file across forges — the engine tests apply
// to each regardless of which forge contributes it.
func allFiles() []File {
	var out []File
	out = append(out, GitHubWorkflows...)
	out = append(out, GitLabCI...)
	out = append(out, InstallScripts...)
	return out
}

func TestAbsentFileIsWritten(t *testing.T) {
	root := t.TempDir()
	for _, f := range allFiles() {
		if res := gen(t, root, f, false); res.Outcome != Written {
			t.Errorf("%s: outcome %v, want Written", f.RelPath, res.Outcome)
		}
		raw, _ := os.ReadFile(filepath.Join(root, f.RelPath))
		s := string(raw)
		if !strings.Contains(s, "DO NOT EDIT") || !strings.Contains(s, "volt:hash ") {
			t.Errorf("%s: header missing", f.RelPath)
		}
	}
}

func TestRegenerateUnedited(t *testing.T) {
	root := t.TempDir()
	f := GitHubWorkflows[0]
	gen(t, root, f, false)
	// Same inputs → byte-identical → Unchanged.
	if res := gen(t, root, f, false); res.Outcome != Unchanged {
		t.Fatalf("outcome %v, want Unchanged", res.Outcome)
	}
	// New volt version changes only the header; hash still matches the body
	// → regeneration proceeds (Written), never Refused.
	v2 := v
	v2.Version = "v0.2.0"
	res, err := Generate(root, f, v2, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Written {
		t.Fatalf("header-only change must regenerate cleanly, got %v (%s)", res.Outcome, res.Diff)
	}
}

// The sync_go incident: a hand edit must be REFUSED with a diff, and --force
// must be the only way through.
func TestHandEditRefusedThenForced(t *testing.T) {
	root := t.TempDir()
	f := GitHubWorkflows[0]
	gen(t, root, f, false)
	p := filepath.Join(root, f.RelPath)
	raw, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(raw), "ubuntu-latest", "macos-latest", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	res := gen(t, root, f, false)
	if res.Outcome != Refused {
		t.Fatalf("hand edit must refuse, got %v", res.Outcome)
	}
	if !strings.Contains(res.Diff, "macos-latest") {
		t.Fatalf("diff must show the hand edit: %s", res.Diff)
	}
	if res := gen(t, root, f, true); res.Outcome != Written {
		t.Fatalf("--force must overwrite, got %v", res.Outcome)
	}
}

// A pre-existing file with NO header is never assumed safe to overwrite.
func TestUnmarkedFileRefused(t *testing.T) {
	root := t.TempDir()
	f := GitHubWorkflows[0]
	p := filepath.Join(root, f.RelPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("name: my own ci\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := gen(t, root, f, false)
	if res.Outcome != Refused || !strings.Contains(res.Diff, "not a file volt generated") {
		t.Fatalf("unmarked file must refuse: %v %s", res.Outcome, res.Diff)
	}
}

func TestInstallShKeepsShebangFirst(t *testing.T) {
	root := t.TempDir()
	gen(t, root, InstallScripts[0], false)
	raw, _ := os.ReadFile(filepath.Join(root, "install.sh"))
	if !strings.HasPrefix(string(raw), "#!/bin/sh\n") {
		t.Fatalf("shebang must stay line 1:\n%s", string(raw)[:60])
	}
	if fi, _ := os.Stat(filepath.Join(root, "install.sh")); fi.Mode()&0o111 == 0 {
		t.Fatal("install.sh must be executable")
	}
}

// The naming contract: the install script must reference the same asset
// shape platform.AssetName produces.
func TestInstallShMatchesAssetNaming(t *testing.T) {
	root := t.TempDir()
	gen(t, root, InstallScripts[0], false)
	raw, _ := os.ReadFile(filepath.Join(root, "install.sh"))
	if !strings.Contains(string(raw), `${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz`) {
		t.Fatal("install.sh asset name diverged from platform.AssetName's contract")
	}
}

// The downgrade guard: an older released volt must not regenerate a newer
// volt's files (released v0.1.0 reverted install.sh's macOS fix in
// volt-demo-cli, 2026-08-22 — this pins the fix).
func TestGenerateRefusesDowngrade(t *testing.T) {
	root := t.TempDir()
	f := GitHubWorkflows[0]
	// Generate with a NEWER release...
	newer := v
	newer.Version = "v0.9.0"
	if res := mustGen(t, root, f, newer, false); res.Outcome != Written {
		t.Fatal(res.Outcome)
	}
	// ...then try to regenerate with an OLDER release: refuse, naming both.
	older := v
	older.Version = "v0.3.0"
	res, err := Generate(root, f, older, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Refused || !strings.Contains(res.Diff, "v0.9.0") || !strings.Contains(res.Diff, "v0.3.0") {
		t.Fatalf("downgrade must refuse naming both versions: %v %s", res.Outcome, res.Diff)
	}
	// --force is the deliberate override.
	if res, _ := Generate(root, f, older, true); res.Outcome != Written {
		t.Fatalf("--force must override: %v", res.Outcome)
	}
}

// A release must refuse dev-stamped files (ordering unknowable); a dev build
// may regenerate anything (the working tree IS the newest code).
func TestGenerateDevStampRules(t *testing.T) {
	root := t.TempDir()
	f := GitHubWorkflows[0]
	dev := v
	dev.Version = "v0.0.0-dev.abc123.dirty"
	mustGen(t, root, f, dev, false)

	release := v
	release.Version = "v0.3.0"
	res, _ := Generate(root, f, release, false)
	if res.Outcome != Refused || !strings.Contains(res.Diff, "dev build") {
		t.Fatalf("release over dev-stamp must refuse: %v %s", res.Outcome, res.Diff)
	}
	// Dev build regenerating release-stamped files: allowed.
	root2 := t.TempDir()
	mustGen(t, root2, f, release, true) // seed with a release stamp
	if res := mustGen(t, root2, f, dev, false); res.Outcome != Written {
		t.Fatalf("dev build must regenerate freely: %v", res.Outcome)
	}
}

func mustGen(t *testing.T, root string, f File, vars Vars, force bool) Result {
	t.Helper()
	res, err := Generate(root, f, vars, force)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// ---- volt gen skills -----------------------------------------------------

var sv = SkillsVars{Binary: "demo", Repo: "khanakia/demo", Tag: "demo/{{version}}", Version: "v0.5.0"}

func TestGenerateSkillsWiring(t *testing.T) {
	root := t.TempDir()
	res, err := GenerateSkillsWiring(root, sv, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Written {
		t.Fatalf("outcome %v", res.Outcome)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "skills_gen.go"))
	s := string(raw)
	for _, want := range []string{"voltkit/skillcmd", `Binary:  "demo"`, `Repo:    "khanakia/demo"`, "volt:hash", "DO NOT EDIT"} {
		if !strings.Contains(s, want) {
			t.Fatalf("wiring missing %q:\n%s", want, s)
		}
	}
	// Hash-guard applies: hand-edit → refuse.
	if err := os.WriteFile(filepath.Join(root, "skills_gen.go"), []byte(strings.Replace(s, "demo", "hacked", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ = GenerateSkillsWiring(root, sv, false)
	if res.Outcome != Refused {
		t.Fatalf("hand-edited wiring must refuse: %v", res.Outcome)
	}
}

// The starter skill is created once and NEVER touched again.
func TestStarterSkillCreatedOnceOnly(t *testing.T) {
	root := t.TempDir()
	dest, err := StarterSkill(root, sv)
	if err != nil {
		t.Fatal(err)
	}
	if dest == "" {
		t.Fatal("first run must create")
	}
	raw, _ := os.ReadFile(dest)
	for _, want := range []string{"name: demo-core", "skills check", "npx skills add khanakia/demo"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("starter missing %q:\n%s", want, raw)
		}
	}
	// User rewrites it; a second run must not touch it.
	if err := os.WriteFile(dest, []byte("user content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest2, err := StarterSkill(root, sv)
	if err != nil || dest2 != "" {
		t.Fatalf("second run must be a no-op: %q %v", dest2, err)
	}
	raw, _ = os.ReadFile(dest)
	if string(raw) != "user content\n" {
		t.Fatal("the project's content was touched")
	}
}

// ---- the skills lint -----------------------------------------------------

func lintFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLintSkillsClean(t *testing.T) {
	dir := lintFixture(t, map[string]string{
		"demo-core/SKILL.md": "---\nname: demo-core\ndescription: does things\n---\nbody",
	})
	problems, warnings, err := LintSkills(dir, "demo")
	if err != nil || len(problems) != 0 || len(warnings) != 0 {
		t.Fatalf("clean skills must pass: %v %v %v", problems, warnings, err)
	}
}

func TestLintSkillsFailures(t *testing.T) {
	dir := lintFixture(t, map[string]string{
		"no-desc/SKILL.md": "---\nname: demo-nodesc\n---\nbody",
		"no-name/SKILL.md": "---\ndescription: d\n---\nbody",
		"empty-dir/notes":  "not a skill",
		"a/SKILL.md":       "---\nname: demo-dup\ndescription: d\n---\n",
		"b/SKILL.md":       "---\nname: demo-dup\ndescription: d\n---\n",
	})
	problems, _, err := LintSkills(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"missing `description:`", "missing `name:`", "no SKILL.md", "duplicate skill name"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("lint must catch %q:\n%s", want, joined)
		}
	}
}

func TestLintSkillsPrefixWarnsOnly(t *testing.T) {
	dir := lintFixture(t, map[string]string{
		"core/SKILL.md": "---\nname: core\ndescription: d\n---\n", // no demo- prefix
	})
	problems, warnings, err := LintSkills(dir, "demo")
	if err != nil || len(problems) != 0 {
		t.Fatalf("prefix must not FAIL: %v %v", problems, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "demo-") {
		t.Fatalf("prefix must WARN naming the convention: %v", warnings)
	}
}

// ---- edge cases: skills helpers ------------------------------------------

// LintSkills on a nonexistent directory — the ReadDir error path.
func TestLintSkillsMissingDir(t *testing.T) {
	if _, _, err := LintSkills(filepath.Join(t.TempDir(), "missing"), "demo"); err == nil {
		t.Fatal("missing skills dir must error (presence gating is the CALLER's job)")
	}
}

// Non-.md loose files and dotfiles in skills/ are ignored by the lint —
// only skill shapes are judged.
func TestLintSkillsIgnoresLooseAndHiddenFiles(t *testing.T) {
	dir := lintFixture(t, map[string]string{
		"demo-core/SKILL.md": "---\nname: demo-core\ndescription: d\n---\n",
		"notes.txt":          "not a skill, not judged",
		".DS_Store":          "junk",
	})
	problems, warnings, err := LintSkills(dir, "demo")
	if err != nil || len(problems) != 0 || len(warnings) != 0 {
		t.Fatalf("%v %v %v", problems, warnings, err)
	}
}

// Single-file sugar goes through the same frontmatter contract.
func TestLintSkillsSingleFileSugar(t *testing.T) {
	dir := lintFixture(t, map[string]string{
		"demo-quick.md": "---\nname: demo-quick\ndescription: d\n---\n",
		"bare.md":       "no frontmatter at all",
	})
	problems, _, err := LintSkills(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "bare.md") {
		t.Fatalf("frontmatterless sugar file must fail the contract: %s", joined)
	}
	if strings.Contains(joined, "demo-quick") {
		t.Fatalf("well-formed sugar must pass: %s", joined)
	}
}

// Empty binary name disables the prefix warning (nothing to prefix with).
func TestLintSkillsNoBinaryNoPrefixWarn(t *testing.T) {
	dir := lintFixture(t, map[string]string{
		"core/SKILL.md": "---\nname: core\ndescription: d\n---\n",
	})
	_, warnings, err := LintSkills(dir, "")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("no binary → no prefix convention to warn about: %v %v", warnings, err)
	}
}

// readFrontmatter: unreadable file errors; quoted + nested handled.
func TestReadFrontmatterEdges(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte("---\nname: 'q'\nmeta:\n  nested: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fm, err := readFrontmatter(p)
	if err != nil || fm["name"] != "q" {
		t.Fatalf("%v %v", fm, err)
	}
	if _, ok := fm["nested"]; ok {
		t.Fatal("nested yaml must be ignored")
	}
	if _, err := readFrontmatter(filepath.Join(dir, "missing.md")); err == nil {
		t.Fatal("unreadable file must error")
	}
}

// StarterSkill when skills/ is CREATABLE but the write target is blocked —
// and the template render path with a hostile binary name (template-safe).
func TestStarterSkillWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o555); err != nil { // read+exec, no write
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	if _, err := StarterSkill(root, sv); err == nil {
		t.Fatal("unwritable root must error")
	}
}

// renderBytes: unknown variable fails loudly (missingkey=error) and bad
// template syntax fails at parse.
func TestRenderBytesFailsLoudly(t *testing.T) {
	if _, err := renderBytes("[[.DoesNotExist]]", Vars{}, nil); err == nil {
		t.Fatal("unknown var must error, never render empty")
	}
	if _, err := renderBytes("[[.Binary", Vars{}, nil); err == nil {
		t.Fatal("bad syntax must error at parse")
	}
	// extras override/extend the base map.
	out, err := renderBytes("[[.Binary]]-[[.Extra]]", Vars{Binary: "b"}, map[string]string{"Extra": "x"})
	if err != nil || string(out) != "b-x" {
		t.Fatalf("%q %v", out, err)
	}
}
