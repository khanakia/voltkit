package skillcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// run executes the skills command tree with args, returning stdout and err.
func run(t *testing.T, o Options, args ...string) (string, error) {
	t.Helper()
	cmd := New(o)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SilenceErrors = true // errors are asserted via the return value
	err := cmd.Execute()
	return out.String(), err
}

// liveOpts serves from a fixture via the env override — the simplest source
// for command-surface tests (cache behaviour has its own suite).
func liveOpts(t *testing.T) Options {
	t.Helper()
	root := skillsFixture(t)
	o := Options{Binary: "demo", Repo: "khanakia/demo", Version: "v1.0.0", CacheRoot: t.TempDir()}
	o.applyDefaults()
	t.Setenv(o.Env, root)
	return o
}

func TestBareSkillsIsList(t *testing.T) {
	out, err := run(t, liveOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lore-core", "lore-search", "quickref", "npx skills add khanakia/demo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list missing %q:\n%s", want, out)
		}
	}
	// First sentence only in the one-line view.
	if strings.Contains(out, "Read this first") {
		t.Fatalf("description must be trimmed to the first sentence:\n%s", out)
	}
}

func TestListJSON(t *testing.T) {
	out, err := run(t, liveOpts(t), "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var skills []Skill
	if err := json.Unmarshal([]byte(out), &skills); err != nil {
		t.Fatalf("not valid json: %v\n%s", err, out)
	}
	if len(skills) != 3 || skills[0].Name != "lore-core" {
		t.Fatalf("%+v", skills)
	}
}

func TestGetSingleWithBanner(t *testing.T) {
	o := liveOpts(t)
	out, err := run(t, o, "get", "lore-core")
	if err != nil {
		t.Fatal(err)
	}
	// The serve banner is the agent's freshness verification — it must
	// state binary and version on the first line.
	if !strings.HasPrefix(out, "<!-- demo v1.0.0") {
		t.Fatalf("banner missing:\n%s", out)
	}
	if !strings.Contains(out, "# core body") {
		t.Fatalf("content missing:\n%s", out)
	}
	// --full not given → references are NOT inlined.
	if strings.Contains(out, "all the commands") {
		t.Fatalf("references must need --full:\n%s", out)
	}
}

func TestGetFullInlinesTextButNotBinary(t *testing.T) {
	o := liveOpts(t)
	// Add a binary reference to the fixture (env override points there).
	root := os.Getenv(o.Env)
	if err := os.WriteFile(filepath.Join(root, "lore-core", "references", "img.png"), []byte{0x89, 0x50, 0x00, 0x47}, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, o, "get", "lore-core", "--full")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--- file: references/commands.md ---") || !strings.Contains(out, "all the commands") {
		t.Fatalf("text reference must inline with a banner:\n%s", out)
	}
	// S3: binary files are LISTED, never inlined.
	if !strings.Contains(out, "references/img.png (binary") || bytes.Contains([]byte(out), []byte{0x89, 0x50, 0x00}) {
		t.Fatalf("binary reference must be listed, not inlined:\n%s", out)
	}
}

func TestGetAllDelimited(t *testing.T) {
	out, err := run(t, liveOpts(t), "get", "--all")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"==> skill: lore-core <==", "==> skill: lore-search <==", "==> skill: quickref <=="} {
		if !strings.Contains(out, want) {
			t.Fatalf("--all must delimit every skill:\n%s", out)
		}
	}
}

func TestGetUnknownNameFails(t *testing.T) {
	_, err := run(t, liveOpts(t), "get", "nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown skill must error naming it: %v", err)
	}
}

func TestGetMultipleNames(t *testing.T) {
	out, err := run(t, liveOpts(t), "get", "lore-core", "quickref")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "core body") || !strings.Contains(out, "quick body") {
		t.Fatalf("multi-get:\n%s", out)
	}
}

func TestPathCommands(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	out, err := run(t, o, "path")
	if err != nil || strings.TrimSpace(out) != root {
		t.Fatalf("bare path must print the serving dir: %q %v", out, err)
	}
	out, _ = run(t, o, "path", "lore-core")
	if strings.TrimSpace(out) != filepath.Join(root, "lore-core") {
		t.Fatalf("skill path: %q", out)
	}
	// Single-file sugar: the path IS the md file.
	out, _ = run(t, o, "path", "quickref")
	if strings.TrimSpace(out) != filepath.Join(root, "quickref.md") {
		t.Fatalf("sugar path: %q", out)
	}
}

func TestVersionCommandJSON(t *testing.T) {
	out, err := run(t, liveOpts(t), "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v["version"] != "v1.0.0" || len(v["skills_hash"]) != 64 || !strings.HasPrefix(v["source"], SourceEnvPrefix) {
		t.Fatalf("%+v", v)
	}
}

func TestCheckCurrentAndStale(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)

	// A faithful installed copy (plus junk) → current, exit 0.
	installed := filepath.Join(t.TempDir(), "lore-core")
	if err := os.CopyFS(installed, os.DirFS(filepath.Join(root, "lore-core"))); err != nil {
		t.Fatal(err)
	}
	write(t, installed, ".DS_Store", "junk")
	out, err := run(t, o, "check", installed)
	if err != nil || !strings.Contains(out, "current") {
		t.Fatalf("faithful copy must be current: %v\n%s", err, out)
	}

	// Tamper → STALE, non-zero, names the file and the refresh path.
	write(t, installed, "SKILL.md", "old stale body")
	out, err = run(t, o, "check", installed)
	if err == nil {
		t.Fatal("stale must exit non-zero")
	}
	for _, want := range []string{"STALE", "SKILL.md", "npx skills add khanakia/demo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stale output missing %q:\n%s", want, out)
		}
	}
}

// check identifies the skill by the INSTALLED frontmatter name even when the
// harness renamed the directory.
func TestCheckUsesFrontmatterName(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	installed := filepath.Join(t.TempDir(), "renamed-by-harness")
	if err := os.CopyFS(installed, os.DirFS(filepath.Join(root, "lore-core"))); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, o, "check", installed); err != nil {
		t.Fatalf("frontmatter name must win over dir name: %v", err)
	}
}

func TestRefreshRefusesOutsideCacheMode(t *testing.T) {
	_, err := run(t, liveOpts(t), "refresh") // env override active
	if err == nil || !strings.Contains(err.Error(), "live directory") {
		t.Fatalf("refresh under env override must refuse: %v", err)
	}
}

func TestDevVersionServesLiveDir(t *testing.T) {
	// A dev build finds skills/ by walking up from the working dir —
	// no fetch, no cache, no env var.
	repo := t.TempDir()
	write(t, repo, "skills/core/SKILL.md", "---\nname: core\ndescription: live\n---\nlive body\n")
	sub := filepath.Join(repo, "cmd", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Binary: "demo", Repo: "khanakia/demo", Version: "v0.0.0-dev.abc.dirty", WorkDir: sub, CacheRoot: t.TempDir()}
	o.applyDefaults()
	out, err := run(t, o, "get", "core")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "live body") || !strings.Contains(out, "source: "+SourceLive) {
		t.Fatalf("dev build must serve the live dir:\n%s", out)
	}
}

func TestDevVersionOutsideRepoErrors(t *testing.T) {
	o := Options{Binary: "demo", Repo: "r", Version: "dev", WorkDir: t.TempDir(), CacheRoot: t.TempDir()}
	o.applyDefaults()
	_, err := run(t, o, "list")
	if err == nil || !strings.Contains(err.Error(), "skills/") {
		t.Fatalf("dev build outside a repo must error clearly: %v", err)
	}
}

// End-to-end through the REAL cache path: cobra command + fake fetcher.
func TestCommandsThroughCachePath(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "---\nname: core\ndescription: cached\n---\ncached body\n"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := Options{Binary: "demo", Repo: "khanakia/demo", Version: "v1.0.0", Fetcher: f, CacheRoot: t.TempDir()}
	o.applyDefaults()
	out, err := run(t, o, "get", "core")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cached body") || !strings.Contains(out, "source: "+SourceCache) {
		t.Fatalf("%s", out)
	}
	// list again — still one fetch total.
	if _, err := run(t, o, "list"); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("expected one fetch across commands, got %d", f.calls)
	}
}

// Guard: cobra must not leak flag state between subcommand constructions.
var _ = cobra.Command{}

// A stale verdict is an operational result, not an argument mistake — the
// usage block must never drown it (caught live in the stool dogfood).
func TestStaleDoesNotPrintUsage(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	installed := filepath.Join(t.TempDir(), "lore-core")
	if err := os.CopyFS(installed, os.DirFS(filepath.Join(root, "lore-core"))); err != nil {
		t.Fatal(err)
	}
	write(t, installed, "SKILL.md", "tampered")
	out, err := run(t, o, "check", installed)
	if err == nil {
		t.Fatal("stale must be an error")
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("usage dump on an operational error:\n%s", out)
	}
}
