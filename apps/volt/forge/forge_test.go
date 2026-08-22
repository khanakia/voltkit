package forge

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ParseRemote pins rule zero of the seam: never guess. A non-GitHub remote
// must return ok=false, because a guessed repo generates wrong URLs into
// files. (Cases migrated from publish's parseGitHubRemote when it moved
// here.)
func TestGitHubParseRemote(t *testing.T) {
	cases := map[string]struct {
		repo Repo
		ok   bool
	}{
		"https://github.com/khanakia/stool.git": {"khanakia/stool", true},
		"https://github.com/khanakia/stool":     {"khanakia/stool", true},
		"git@github.com:khanakia/stool.git":     {"khanakia/stool", true},
		"https://gitlab.com/x/y.git":            {"", false}, // non-GitHub: never guess
		"https://github.com/only-owner":         {"", false},
		"":                                      {"", false},
	}
	for in, want := range cases {
		got, ok := GitHub{}.ParseRemote(in)
		if got != want.repo || ok != want.ok {
			t.Errorf("ParseRemote(%q) = (%q, %v), want (%q, %v)", in, got, ok, want.repo, want.ok)
		}
	}
}

// The URL shapes are load-bearing contracts: install scripts, self-update
// and skillcmd fetching all depend on the browser-download redirect (no API
// rate limit). A silent shape change breaks every installed binary's
// self-update, so the exact strings are pinned.
func TestGitHubURLShapes(t *testing.T) {
	g := GitHub{}
	if got, want := g.AssetURL("khanakia/voltkit", "volt/v0.5.0", "checksums.txt"),
		"https://github.com/khanakia/voltkit/releases/download/volt/v0.5.0/checksums.txt"; got != want {
		t.Errorf("AssetURL = %q, want %q", got, want)
	}
	if got, want := g.LatestAssetURL("khanakia/stool", "stool_darwin_arm64.tar.gz"),
		"https://github.com/khanakia/stool/releases/latest/download/stool_darwin_arm64.tar.gz"; got != want {
		t.Errorf("LatestAssetURL = %q, want %q", got, want)
	}
}

// ByName: the future `forge:` override resolver. A typo must error and the
// error must enumerate valid names — silent fallback would publish to the
// wrong forge.
func TestByName(t *testing.T) {
	f, err := ByName("github")
	if err != nil || f.Name() != "github" {
		t.Fatalf("ByName(github) = %v, %v", f, err)
	}
	if _, err := ByName("gitlub"); err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("a typo must error AND name the valid forges, got %v", err)
	}
}

// Detect pins the FG-D6 rules: no remote → Default (local-only repos still
// build); recognized host → that forge, zero config; unrecognized host →
// hard error naming the `forge:` fix (guessing publishes to the wrong
// forge); explicit override outranks the remote.
func TestDetect(t *testing.T) {
	dir := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q")

	if f, err := Detect(dir, ""); err != nil || f.Name() != Default().Name() {
		t.Fatalf("no remote must fall back to Default, got %v, %v", f, err)
	}

	mustGit("remote", "add", "origin", "https://github.com/khanakia/x.git")
	if f, err := Detect(dir, ""); err != nil || f.Name() != "github" {
		t.Fatalf("GitHub remote must detect github, got %v, %v", f, err)
	}

	mustGit("remote", "set-url", "origin", "https://gitlab.com/khanakia/x.git")
	if f, err := Detect(dir, ""); err != nil || f.Name() != "gitlab" {
		t.Fatalf("GitLab remote must detect gitlab, got %v, %v", f, err)
	}

	mustGit("remote", "set-url", "origin", "https://git.mycompany.com/khanakia/x.git")
	if _, err := Detect(dir, ""); err == nil || !strings.Contains(err.Error(), "forge:") {
		t.Fatalf("unknown host must hard-error naming the forge: fix, got %v", err)
	}
	// The override rescues the self-hosted case — and outranks the remote.
	if f, err := Detect(dir, "gitlab"); err != nil || f.Name() != "gitlab" {
		t.Fatalf("override must win, got %v, %v", f, err)
	}
	if _, err := Detect(dir, "gitlub"); err == nil {
		t.Fatal("override typo must error, never fall back")
	}
}

// GitLab remote parsing: nested subgroup paths are LEGAL repo identities on
// GitLab — the owner/name two-segment assumption is GitHub's, not the seam's.
func TestGitLabParseRemote(t *testing.T) {
	cases := map[string]struct {
		repo Repo
		ok   bool
	}{
		"https://gitlab.com/khanakia/stool.git":  {"khanakia/stool", true},
		"git@gitlab.com:khanakia/stool.git":      {"khanakia/stool", true},
		"https://gitlab.com/group/subgroup/proj": {"group/subgroup/proj", true},
		"https://github.com/khanakia/stool.git":  {"", false}, // never guess
		"https://gitlab.com/only-owner":          {"", false},
		"":                                       {"", false},
	}
	for in, want := range cases {
		got, ok := GitLab{}.ParseRemote(in)
		if got != want.repo || ok != want.ok {
			t.Errorf("ParseRemote(%q) = (%q, %v), want (%q, %v)", in, got, ok, want.repo, want.ok)
		}
	}
}

// GitLab URL shapes are pinned like GitHub's: the release permanent-link
// contract (FG-D7) is what install scripts and skills fetching rely on.
func TestGitLabURLShapes(t *testing.T) {
	g := GitLab{}
	if got, want := g.AssetURL("khanakia/demo", "notes/v1.0.0", "checksums.txt"),
		"https://gitlab.com/khanakia/demo/-/releases/notes/v1.0.0/downloads/checksums.txt"; got != want {
		t.Errorf("AssetURL = %q, want %q", got, want)
	}
	if got, want := g.LatestAssetURL("khanakia/demo", "x.tar.gz"),
		"https://gitlab.com/khanakia/demo/-/releases/permalink/latest/downloads/x.tar.gz"; got != want {
		t.Errorf("LatestAssetURL = %q, want %q", got, want)
	}
	if got, want := g.FileURL("khanakia/demo", "CHANGELOG.md"),
		"https://gitlab.com/khanakia/demo/-/blob/main/CHANGELOG.md"; got != want {
		t.Errorf("FileURL = %q, want %q", got, want)
	}
}

// RepoOf's URL-parsing rung must work on a repo that has a remote but was
// never pushed (gh cannot see it) — the `volt gen` on a fresh repo case.
func TestGitHubRepoOfParsesUnpushedRemote(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:khanakia/fresh.git"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// gh may or may not be installed where tests run; the fallback rung must
	// answer regardless. filepath.EvalSymlinks avoids /tmp vs /private/tmp
	// mismatches confusing gh's directory handling on macOS.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	if got := (GitHub{}).RepoOf(resolved); got != "khanakia/fresh" {
		t.Fatalf("RepoOf = %q, want khanakia/fresh", got)
	}
}

// The Publisher handed out by the forge must satisfy the seam's interface —
// the typed function value is the compile-time assertion; the run pins
// non-nil.
func TestGitHubPublisherIsTheGHDriver(t *testing.T) {
	get := func(g GitHub) Publisher { return g.Publisher(".") }
	if get(GitHub{}) == nil {
		t.Fatal("Publisher must never be nil")
	}
}

// Each forge's CI file set is a contract with `volt gen`: file paths decide
// which runner even sees the repo, so the sets are pinned by name.
func TestCIFileSets(t *testing.T) {
	gh := GitHub{}.CIFiles()
	if len(gh) != 2 || gh[0].RelPath != ".github/workflows/ci.yml" || gh[1].RelPath != ".github/workflows/release.yml" {
		t.Fatalf("GitHub CI set changed unexpectedly: %+v", gh)
	}
	gl := GitLab{}.CIFiles()
	if len(gl) != 1 || gl[0].RelPath != ".gitlab-ci.yml" {
		t.Fatalf("GitLab CI set changed unexpectedly: %+v", gl)
	}
}

// The remaining URL shapes are contracts too: changelog links in release
// notes, scaffold's template fetch, the raw "curl | sh" hints. Pinned so a
// shape change is a deliberate test change.
func TestAuxiliaryURLShapes(t *testing.T) {
	gh := GitHub{}
	for got, want := range map[string]string{
		gh.FileURL("o/n", "CHANGELOG.md"):        "https://github.com/o/n/blob/main/CHANGELOG.md",
		gh.RawFileURL("o/n", "install.sh"):       "https://raw.githubusercontent.com/o/n/main/install.sh",
		gh.ArchiveTarballURL("o/n", "abc123"):    "https://codeload.github.com/o/n/tar.gz/abc123",
		gh.CloneURL("o/n"):                       "https://github.com/o/n.git",
		GitLab{}.RawFileURL("o/n", "install.sh"): "https://gitlab.com/o/n/-/raw/main/install.sh",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// Doctor's contract is shape, not environment: at least one probe, and every
// failing probe names its fix (the doctor promise). Environment-independent —
// asserts hold whether or not the CLIs are installed.
func TestDoctorChecksNameTheirFix(t *testing.T) {
	for _, f := range []Forge{GitHub{}, GitLab{}} {
		checks := f.Doctor()
		if len(checks) == 0 {
			t.Fatalf("%s: Doctor returned no probes", f.Name())
		}
		for _, c := range checks {
			if c.Good == "" || c.Bad == "" {
				t.Fatalf("%s: every check needs both messages, got %+v", f.Name(), c)
			}
		}
	}
}

// Publishers from both forges satisfy the seam interface and are never nil.
func TestPublishersNonNil(t *testing.T) {
	for _, f := range []Forge{GitHub{}, GitLab{}} {
		if f.Publisher(".") == nil {
			t.Fatalf("%s: nil publisher", f.Name())
		}
	}
}
