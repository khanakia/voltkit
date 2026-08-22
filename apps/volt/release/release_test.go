package release

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakePub is an in-memory Publisher.
type fakePub struct {
	bodies map[string]string
	assets map[string][]string
	fail   bool
}

func newFakePub() *fakePub {
	return &fakePub{bodies: map[string]string{}, assets: map[string][]string{}}
}
func (f *fakePub) ReleaseExists(tag string) bool { _, ok := f.bodies[tag]; return ok }
func (f *fakePub) CreateOrUpdate(tag, title, notesFile string, assets []string) error {
	if f.fail {
		return os.ErrDeadlineExceeded
	}
	b, err := os.ReadFile(notesFile)
	if err != nil {
		return err
	}
	f.bodies[tag] = string(b)
	for _, a := range assets {
		f.assets[tag] = append(f.assets[tag], filepath.Base(a))
	}
	return nil
}
func (f *fakePub) FetchBody(tag string) (string, error)         { return f.bodies[tag], nil }
func (f *fakePub) FetchAssetNames(tag string) ([]string, error) { return f.assets[tag], nil }

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo scaffolds a committed monorepo — cmd/notes (CLI) + version/ (library)
// — with a local bare origin, host-only platforms for build speed.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/repo\n\ngo 1.22\n")
	write("cmd/notes/main.go", "package main\n\nvar version = \"dev\"\n\nfunc main() { println(version) }\n")
	write("cmd/notes/.volt.yml", hostPlatformYML()+"ldflags:\n  vars:\n    main.version: \"{{.Version}}\"\n")
	write("cmd/notes/CHANGELOG.md", "# Changelog\n\n## [1.4.0]\n\n- shipped the thing\n")
	write("version/lib.go", "package version\n\nfunc V() int { return 1 }\n")
	write("version/lib_test.go", "package version\n\nimport \"testing\"\n\nfunc TestV(t *testing.T) { if V() != 1 { t.Fail() } }\n")
	mustGit(t, root, "init", "-q")
	mustGit(t, root, "config", "user.email", "t@t")
	mustGit(t, root, "config", "user.name", "t")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "init")
	bare := t.TempDir()
	mustGit(t, bare, "init", "-q", "--bare")
	mustGit(t, root, "remote", "add", "origin", bare)
	return root
}

func TestReleaseCLIEndToEnd(t *testing.T) {
	root := repo(t)
	pub := newFakePub()
	var log bytes.Buffer
	res, err := Run(Options{
		Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: pub, Repo: "khanakia/notes",
		DistDir: filepath.Join(t.TempDir(), "dist"), Log: &log,
	})
	if err != nil {
		t.Fatalf("%v\nlog:\n%s", err, log.String())
	}
	if res.Tag != "notes/v1.4.0" {
		t.Fatalf("tag = %q", res.Tag)
	}
	// The CHANGELOG section, not the fallback, became the notes.
	if !strings.Contains(pub.bodies["notes/v1.4.0"], "shipped the thing") {
		t.Fatalf("notes wrong: %q", pub.bodies["notes/v1.4.0"])
	}
	// Binary archive + checksums both published.
	names := strings.Join(pub.assets["notes/v1.4.0"], " ")
	if !strings.Contains(names, "notes_v1.4.0_") || !strings.Contains(names, "checksums.txt") {
		t.Fatalf("assets wrong: %v", pub.assets["notes/v1.4.0"])
	}
}

func TestReleaseLibraryPublishesNoBinaries(t *testing.T) {
	root := repo(t)
	pub := newFakePub()
	res, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: pub})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tag != "version/v0.3.0" {
		t.Fatalf("library tag must be the directory path, got %q", res.Tag)
	}
	if len(pub.assets["version/v0.3.0"]) != 0 {
		t.Fatalf("library release must carry no assets: %v", pub.assets)
	}
}

func TestDirtyTreeRefused(t *testing.T) {
	root := repo(t)
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: newFakePub()})
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty tree must refuse: %v", err)
	}
}

// Failing tests must leave NOTHING permanent — no tag anywhere.
func TestFailingTestsCreateNoTag(t *testing.T) {
	root := repo(t)
	if err := os.WriteFile(filepath.Join(root, "version/lib_test.go"),
		[]byte("package version\n\nimport \"testing\"\n\nfunc TestV(t *testing.T) { t.Fatal(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "break")
	_, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: newFakePub()})
	if err == nil {
		t.Fatal("want test failure")
	}
	out, _ := exec.Command("git", "-C", root, "tag", "-l").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("failing tests must tag nothing, got %q", out)
	}
}

// Publish failure AFTER the tag exists: error must name the recovery
// (--from-tag), and the from-tag path must then succeed idempotently.
func TestFromTagRecoversFailedPublish(t *testing.T) {
	root := repo(t)
	pub := newFakePub()
	pub.fail = true
	_, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: pub})
	if err == nil || !strings.Contains(err.Error(), "--from-tag") {
		t.Fatalf("failure after tagging must name the recovery: %v", err)
	}
	pub.fail = false
	res, err := Run(Options{Root: root, FromTag: "version/v0.3.0", Publisher: pub, SkipTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tag != "version/v0.3.0" || pub.bodies["version/v0.3.0"] == "" {
		t.Fatalf("from-tag republish failed: %+v", res)
	}
}

func TestSnapshotPublishesNothingKeepsDist(t *testing.T) {
	root := repo(t)
	// Snapshot is exempt from the dirty check — dirty the tree to prove it.
	if err := os.WriteFile(filepath.Join(root, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pub := newFakePub()
	dist := filepath.Join(t.TempDir(), "dist")
	res, err := Run(Options{Root: root, Dir: "cmd/notes", Snapshot: true, Publisher: pub, DistDir: dist})
	if err != nil {
		t.Fatal(err)
	}
	if len(pub.bodies) != 0 {
		t.Fatal("snapshot must publish nothing")
	}
	if !strings.HasPrefix(res.Tag, SnapshotPrefix) {
		t.Fatalf("snapshot version shape: %q", res.Tag)
	}
	if len(res.Assets) == 0 {
		t.Fatal("snapshot must keep dist/ artifacts")
	}
	// Version verification: the snapshot pseudo-version must be stamped.
	if _, err := os.Stat(filepath.Join(dist, res.Assets[0])); err != nil {
		t.Fatal(err)
	}
}

// Verification failure is an error even though publish "succeeded".
func TestVerificationFailureIsAnError(t *testing.T) {
	root := repo(t)
	pub := newFakePub()
	if _, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: pub}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the published body, then republish via from-tag with a
	// publisher that no-ops the update (simulates GitHub mangling).
	pub2 := &corruptingPub{fakePub: pub}
	_, err := Run(Options{Root: root, FromTag: "version/v0.3.0", Publisher: pub2, SkipTests: true})
	if err == nil || !strings.Contains(err.Error(), "verification FAILED") {
		t.Fatalf("mangled body must fail verification: %v", err)
	}
}

// corruptingPub accepts the publish but stores a mangled body.
type corruptingPub struct{ *fakePub }

func (c *corruptingPub) CreateOrUpdate(tag, title, notesFile string, assets []string) error {
	c.bodies[tag] = "MANGLED BY THE PLATFORM"
	return nil
}

// --from-artifacts must refuse a partial platform set — a partial release
// looks successful and the tag is permanent.
func TestFromArtifactsRefusesPartialSet(t *testing.T) {
	root := repo(t)
	// Config demands two platforms; provide an archive for only one.
	if err := os.WriteFile(filepath.Join(root, "cmd/notes/.volt.yml"),
		[]byte("platforms: [linux/amd64, darwin/arm64]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "cfg")
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "notes_v1.4.0_linux_amd64.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: newFakePub(), DistDir: dist, FromArtifacts: true})
	if err == nil || !strings.Contains(err.Error(), "darwin/arm64") {
		t.Fatalf("partial set must be refused naming the missing platform: %v", err)
	}
}

// A complete set publishes with fresh cross-set checksums.
func TestFromArtifactsCompleteSetPublishes(t *testing.T) {
	root := repo(t)
	if err := os.WriteFile(filepath.Join(root, "cmd/notes/.volt.yml"),
		[]byte("platforms: [linux/amd64, darwin/arm64]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "cfg")
	dist := t.TempDir()
	for _, n := range []string{"notes_v1.4.0_linux_amd64.tar.gz", "notes_v1.4.0_darwin_arm64.tar.gz"} {
		if err := os.WriteFile(filepath.Join(dist, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pub := newFakePub()
	res, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: pub, DistDir: dist, FromArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Join(pub.assets[res.Tag], " ")
	if !strings.Contains(names, "checksums.txt") || !strings.Contains(names, "darwin_arm64") {
		t.Fatalf("assets: %v", pub.assets[res.Tag])
	}
}

// Proxy warm-up: failure is a warning normally, an error under --strict.
func TestProxyWarmupWarnsThenStrictFails(t *testing.T) {
	root := repo(t)
	failing := func(module, version string) error { return os.ErrNotExist }
	res, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0",
		Publisher: newFakePub(), WarmProxy: failing})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "proxy warm-up") {
		t.Fatalf("proxy failure must warn: %v", res.Warnings)
	}
	_, err = Run(Options{Root: root, FromTag: "version/v0.3.0", SkipTests: true,
		Publisher: newFakePub(), WarmProxy: failing, Strict: true})
	if err == nil || !strings.Contains(err.Error(), "proxy warm-up") {
		t.Fatalf("--strict must promote the warning to an error: %v", err)
	}
}

// Homebrew channel: pushes the rendered formula on success; a push failure
// is a skipped channel, not a failed release.
func TestBrewChannelGating(t *testing.T) {
	root := repo(t)
	var pushed string
	ok := func(tap, binary, formula, message string) error { pushed = formula; return nil }
	res, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: newFakePub(), DistDir: filepath.Join(t.TempDir(), "d"),
		Brew:        release_BrewConfig{Tap: "khanakia/homebrew-tap", Description: "notes"},
		PushFormula: ok})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pushed, "class Notes < Formula") {
		t.Fatalf("formula not rendered/pushed:\n%s", pushed)
	}
	// Failing push → warning, release still succeeds.
	failing := func(tap, binary, formula, message string) error { return os.ErrPermission }
	res, err = Run(Options{Root: root, FromTag: "notes/v1.4.0", SkipTests: true,
		Publisher: newFakePub(), DistDir: filepath.Join(t.TempDir(), "d2"),
		Brew:        release_BrewConfig{Tap: "khanakia/homebrew-tap"},
		PushFormula: failing})
	if err != nil {
		t.Fatalf("brew failure must not fail the release: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "homebrew channel skipped") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skip must be loud: %v", res.Warnings)
	}
}

// A directory marked internal must refuse to release, naming the marker.
func TestInternalDirRefusesRelease(t *testing.T) {
	root := repo(t)
	if err := os.WriteFile(filepath.Join(root, "version/.volt.yml"), []byte("internal: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "mark internal")
	_, err := Run(Options{Root: root, Dir: "version", Version: "v0.9.0", Publisher: newFakePub()})
	if err == nil || !strings.Contains(err.Error(), "internal") {
		t.Fatalf("want internal refusal, got %v", err)
	}
}

// A CLI release whose repo carries managed skills must attach the bundle;
// the extracted shape must be what skillcmd's cache expects (skills/ wrapper).
func TestReleaseAttachesSkillsBundle(t *testing.T) {
	root := repo(t)
	if err := os.MkdirAll(filepath.Join(root, "skills", "notes-core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "notes-core", "SKILL.md"),
		[]byte("---\nname: notes-core\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// OS junk in skills/ must not ship.
	if err := os.WriteFile(filepath.Join(root, "skills", ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "skills")

	pub := newFakePub()
	dist := filepath.Join(t.TempDir(), "dist")
	res, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: pub, DistDir: dist})
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Join(pub.assets[res.Tag], " ")
	if !strings.Contains(names, "skills_v1.4.0.tar.gz") {
		t.Fatalf("bundle not attached: %v", pub.assets[res.Tag])
	}
	// The bundle's bytes must extract to skills/<skill>/SKILL.md — and
	// exclude the junk.
	raw, err := os.ReadFile(filepath.Join(dist, "skills_v1.4.0.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	listing := tarList(t, raw)
	if !listing["skills/notes-core/SKILL.md"] {
		t.Fatalf("bundle shape wrong: %v", listing)
	}
	for name := range listing {
		if strings.Contains(name, ".DS_Store") {
			t.Fatalf("junk shipped in the bundle: %v", listing)
		}
	}
}

// A library release never attaches a bundle (no binary to serve skills).
func TestLibraryReleaseSkipsSkillsBundle(t *testing.T) {
	root := repo(t)
	if err := os.MkdirAll(filepath.Join(root, "skills", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "x", "SKILL.md"), []byte("---\nname: x\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "skills")
	pub := newFakePub()
	res, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: pub})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range pub.assets[res.Tag] {
		if strings.Contains(a, "skills_") {
			t.Fatalf("library must not attach a skills bundle: %v", pub.assets[res.Tag])
		}
	}
}

// skills.managed: false → no bundle, even for a CLI.
func TestUnmanagedSkillsNotBundled(t *testing.T) {
	root := repo(t)
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "notes", ".volt.yml"),
		[]byte(hostPlatformYML()+"skills:\n  managed: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "cfg")
	pub := newFakePub()
	res, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: pub, DistDir: filepath.Join(t.TempDir(), "d")})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range pub.assets[res.Tag] {
		if strings.Contains(a, "skills_") {
			t.Fatalf("unmanaged skills must not bundle: %v", pub.assets[res.Tag])
		}
	}
}

// buildSkillsBundle prefers the RELEASED DIRECTORY's skills/ over the repo
// root's — a monorepo CLI owns its own skills.
func TestSkillsBundlePrefersReleasedDir(t *testing.T) {
	root := repo(t)
	// Both exist; the per-CLI one must win.
	for base, marker := range map[string]string{
		"cmd/notes/skills/notes-core": "PER-CLI",
		"skills/notes-core":           "ROOT",
	} {
		if err := os.MkdirAll(filepath.Join(root, base), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, base, "SKILL.md"),
			[]byte("---\nname: notes-core\ndescription: d\n---\n"+marker), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "skills")
	dist := filepath.Join(t.TempDir(), "dist")
	if _, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: newFakePub(), DistDir: dist}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dist, "skills_v1.4.0.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	// Extract mentally: the content must be the per-CLI marker.
	if !strings.Contains(string(decompress(t, raw)), "PER-CLI") {
		t.Fatal("released-dir skills must win over root skills")
	}
}

// An EMPTY managed skills dir fails the release loudly (empty bundle ban),
// rather than publishing a release whose skills serve nothing.
func TestSkillsBundleEmptyDirFailsRelease(t *testing.T) {
	root := repo(t)
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "empty skills")
	_, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: newFakePub(), DistDir: filepath.Join(t.TempDir(), "d")})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty skills dir must fail the release: %v", err)
	}
}

// ---- release hooks -------------------------------------------------------

// hookScript writes an executable script into the repo and returns its
// repo-relative path. The script appends its VOLT_* env view to a log file
// so tests can assert both THAT it ran and WHAT it was told.
func hookScript(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return "./" + rel
}

// withHooks writes the released dir's .volt.yml with hook config and commits.
func withHooks(t *testing.T, root, dir, yml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, dir, ".volt.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "hooks")
}

// post_release: runs after a successful release, receives the full VOLT_*
// context, and executes from the repo root.
func TestPostReleaseHookRunsWithEnv(t *testing.T) {
	root := repo(t)
	evidence := filepath.Join(t.TempDir(), "hook-evidence.txt")
	t.Setenv("HOOK_EVIDENCE", evidence)
	hookScript(t, root, "scripts/post.sh",
		"pwd > \"$HOOK_EVIDENCE\"\nenv | grep '^VOLT_' | sort >> \"$HOOK_EVIDENCE\"\n")
	withHooks(t, root, "version", "hooks:\n  post_release: ./scripts/post.sh\n")
	if _, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: newFakePub()}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal("hook did not run:", err)
	}
	got := string(raw)
	rootResolved, _ := filepath.EvalSymlinks(root)
	for _, want := range []string{
		rootResolved + "\n", // cwd = repo root
		"VOLT_HOOK=post_release", "VOLT_TAG=version/v0.3.0", "VOLT_VERSION=v0.3.0",
		"VOLT_DIR=version", "VOLT_KIND=library",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("hook context missing %q:\n%s", want, got)
		}
	}
}

// pre_release failure: the release aborts with NOTHING permanent — no tag,
// locally or on the remote.
func TestPreReleaseHookAbortsBeforeTag(t *testing.T) {
	root := repo(t)
	hookScript(t, root, "scripts/gate.sh", "echo 'gate says no' >&2\nexit 1\n")
	withHooks(t, root, "version", "hooks:\n  pre_release: ./scripts/gate.sh\n")
	_, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: newFakePub()})
	if err == nil || !strings.Contains(err.Error(), "pre_release hook refused") {
		t.Fatalf("want the refusal error, got %v", err)
	}
	out, _ := exec.Command("git", "-C", root, "tag", "-l").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("a refused release must tag NOTHING, got %q", out)
	}
}

// pre_release ordering: when it passes, it ran BEFORE the tag existed —
// yet it still SEES the composed tag name via VOLT_TAG (a gate that logs
// "what is about to be tagged" needs the name before anything is created).
func TestPreReleaseHookRunsBeforeTagExists(t *testing.T) {
	root := repo(t)
	tagsFile := filepath.Join(t.TempDir(), "tags-at-hook-time.txt")
	t.Setenv("HOOK_EVIDENCE", tagsFile)
	hookScript(t, root, "scripts/pre.sh",
		"echo \"existing=$(git tag -l | tr '\\n' ' ')\" > \"$HOOK_EVIDENCE\"\necho \"tag=$VOLT_TAG\" >> \"$HOOK_EVIDENCE\"\n")
	withHooks(t, root, "version", "hooks:\n  pre_release: ./scripts/pre.sh\n")
	if _, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: newFakePub()}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(tagsFile)
	got := string(raw)
	if !strings.Contains(got, "existing=\n") && !strings.Contains(got, "existing= \n") {
		t.Fatalf("pre_release must run before ANY tag exists, saw:\n%s", got)
	}
	if !strings.Contains(got, "tag=version/v0.3.0") {
		t.Fatalf("pre_release must see the composed VOLT_TAG, got:\n%s", got)
	}
}

// post_release failure: the release itself already succeeded and stays —
// the error says exactly that, and the tag + publish survive.
func TestPostReleaseHookFailureAfterSuccess(t *testing.T) {
	root := repo(t)
	hookScript(t, root, "scripts/boom.sh", "exit 7\n")
	withHooks(t, root, "version", "hooks:\n  post_release: ./scripts/boom.sh\n")
	pub := newFakePub()
	_, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: pub})
	if err == nil || !strings.Contains(err.Error(), "SUCCEEDED") {
		t.Fatalf("error must state the release stands: %v", err)
	}
	if pub.bodies["version/v0.3.0"] == "" {
		t.Fatal("the release must have published despite the hook failure")
	}
	out, _ := exec.Command("git", "-C", root, "tag", "-l").Output()
	if !strings.Contains(string(out), "version/v0.3.0") {
		t.Fatal("the tag must survive a post hook failure")
	}
}

// --snapshot runs NO hooks: it is a dry-run that publishes nothing, and a
// promote-style post hook firing on a dry-run would be a disaster.
func TestSnapshotSkipsHooks(t *testing.T) {
	root := repo(t)
	evidence := t.TempDir()
	t.Setenv("HOOK_EVIDENCE", evidence)
	hookScript(t, root, "scripts/never.sh", "touch \"$HOOK_EVIDENCE/HOOK-RAN\"\n")
	withHooks(t, root, "cmd/notes",
		hostPlatformYML()+"hooks:\n  pre_release: ./scripts/never.sh\n  post_release: ./scripts/never.sh\n")
	if _, err := Run(Options{Root: root, Dir: "cmd/notes", Snapshot: true,
		Publisher: newFakePub(), DistDir: filepath.Join(t.TempDir(), "d")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(evidence, "HOOK-RAN")); !os.IsNotExist(err) {
		t.Fatal("snapshot must never run hooks")
	}
}

// --from-tag: pre is skipped (the tag exists; nothing left to gate), post
// runs (finishing publication is exactly what a republish is for).
func TestFromTagSkipsPreRunsPost(t *testing.T) {
	root := repo(t)
	// Evidence lands OUTSIDE the repo: a hook writing into the tree would
	// dirty it and the second release would (correctly!) refuse — the dirty
	// check guards real releases the same way.
	evidence := t.TempDir()
	t.Setenv("HOOK_EVIDENCE", evidence)
	hookScript(t, root, "scripts/pre.sh", "touch \"$HOOK_EVIDENCE/PRE-RAN\"\n")
	hookScript(t, root, "scripts/post.sh", "echo again >> \"$HOOK_EVIDENCE/POST-COUNT\"\n")
	withHooks(t, root, "version",
		"hooks:\n  pre_release: ./scripts/pre.sh\n  post_release: ./scripts/post.sh\n")
	pub := newFakePub()
	if _, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: pub}); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(evidence, "PRE-RAN"))
	if _, err := Run(Options{Root: root, FromTag: "version/v0.3.0", SkipTests: true, Publisher: pub}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(evidence, "PRE-RAN")); !os.IsNotExist(err) {
		t.Fatal("--from-tag must skip pre_release")
	}
	raw, _ := os.ReadFile(filepath.Join(evidence, "POST-COUNT"))
	if strings.Count(string(raw), "again") != 2 {
		t.Fatalf("post_release must run on the republish too: %q", raw)
	}
}

// A missing hook script is a clear error; non-executable gets the chmod hint.
func TestHookScriptErrors(t *testing.T) {
	root := repo(t)
	withHooks(t, root, "version", "hooks:\n  pre_release: ./scripts/ghost.sh\n")
	_, err := Run(Options{Root: root, Dir: "version", Version: "v0.3.0", Publisher: newFakePub()})
	if err == nil || !strings.Contains(err.Error(), "ghost.sh") {
		t.Fatalf("missing script must error naming it: %v", err)
	}

	root2 := repo(t)
	p := filepath.Join(root2, "scripts", "noexec.sh")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\ntrue\n"), 0o644); err != nil { // no +x
		t.Fatal(err)
	}
	withHooks(t, root2, "version", "hooks:\n  pre_release: ./scripts/noexec.sh\n")
	_, err = Run(Options{Root: root2, Dir: "version", Version: "v0.3.0", Publisher: newFakePub()})
	if err == nil || !strings.Contains(err.Error(), "chmod +x") {
		t.Fatalf("non-executable must hint chmod +x: %v", err)
	}
}

// checksums.txt must cover EVERY published asset — including the skills
// bundle, which skillcmd's fetch verifies against it: a bundle absent from
// checksums.txt makes every consumer fetch refuse ("no entry").
func TestChecksumsCoverSkillsBundle(t *testing.T) {
	root := repo(t)
	if err := os.MkdirAll(filepath.Join(root, "skills", "notes-core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "notes-core", "SKILL.md"),
		[]byte("---\nname: notes-core\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "skills")
	dist := filepath.Join(t.TempDir(), "dist")
	if _, err := Run(Options{Root: root, Dir: "cmd/notes", Version: "v1.4.0",
		Publisher: newFakePub(), DistDir: dist}); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sums), "skills_v1.4.0.tar.gz") {
		t.Fatalf("checksums.txt must include the skills bundle:\n%s", sums)
	}
}
