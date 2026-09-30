package genfiles

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installFixture is a fake release server: a local git repository carrying
// tags from several streams (the source `git ls-remote` reads), and a
// directory per tag holding the archive and checksums.txt the way a volt
// release publishes them, served to the script as file://.
type installFixture struct {
	repo, rel string
	os, arch  string
}

func newInstallFixture(t *testing.T) installFixture {
	t.Helper()
	for _, tool := range []string{"sh", "git", "tar", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is the macOS/Linux installer")
	}
	root := t.TempDir()
	fx := installFixture{repo: filepath.Join(root, "repo"), rel: filepath.Join(root, "rel"), os: runtime.GOOS, arch: runtime.GOARCH}
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(fx.repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(fx.repo, "git", "init", "-q")
	run(fx.repo, "git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x")
	// notes/v0.10.0 must beat notes/v0.9.0 (numeric, not lexical); the other
	// streams' tags are newer and must be ignored.
	for _, tag := range []string{"notes/v0.2.0", "notes/v0.9.0", "notes/v0.10.0", "other/v9.0.0", "v5.0.0", "notes/v1.0.0-rc.1"} {
		run(fx.repo, "git", "tag", tag)
	}
	for _, tag := range []string{"notes/v0.2.0", "notes/v0.10.0", "v5.0.0"} {
		fx.publish(t, tag, true)
	}
	return fx
}

// publish lays out one release: the archive for this machine (when withAsset)
// and checksums.txt listing it.
func (fx installFixture) publish(t *testing.T, tag string, withAsset bool) {
	t.Helper()
	version := tag[strings.LastIndex(tag, "/")+1:]
	dir := filepath.Join(fx.rel, filepath.FromSlash(tag))
	pkg := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := "notes"
	if !strings.HasPrefix(tag, "notes/") {
		bin = "root"
	}
	script := fmt.Sprintf("#!/bin/sh\necho %s %s\n", bin, tag)
	if err := os.WriteFile(filepath.Join(pkg, "notes"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	asset := fmt.Sprintf("notes_%s_%s_%s.tar.gz", version, fx.os, fx.arch)
	if !withAsset {
		asset = fmt.Sprintf("notes_%s_plan9_mips.tar.gz", version)
	}
	// A companion program in the same archive, as extra_binaries packs it.
	if err := os.WriteFile(filepath.Join(pkg, "notes-sync"), []byte("#!/bin/sh\necho sync "+tag+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("tar", "czf", filepath.Join(dir, asset), "notes", "notes-sync")
	c.Dir = pkg
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, asset))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", sum, asset)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// script generates install.sh for the fixture with the given tag prefix.
func (fx installFixture) script(t *testing.T, prefix string, extras ...string) string {
	t.Helper()
	root := t.TempDir()
	vars := Vars{
		Repo: "o/n", Binary: "notes", Version: "v0.1.0",
		DownloadBase: "file://" + fx.rel + "/${TAG}", LatestBase: "",
		RawScriptURL: "https://example.invalid/install.sh", TagPrefix: prefix, CloneURL: fx.repo,
		ExtraBinaries: extras,
	}
	if _, err := Generate(root, InstallScripts[0], vars, false); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "install.sh")
}

func (fx installFixture) install(t *testing.T, script string, env ...string) (string, string, error) {
	t.Helper()
	dest := t.TempDir()
	c := exec.Command("sh", script)
	c.Env = append(append(os.Environ(), "INSTALL_DIR="+dest), env...)
	out, err := c.CombinedOutput()
	return dest, string(out), err
}

func installed(t *testing.T, dest string) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(dest, "notes")).CombinedOutput()
	if err != nil {
		return "(not installed)"
	}
	return strings.TrimSpace(string(out))
}

// TestInstallShResolvesItsOwnTagStream pins the multi-stream fix: a CLI whose
// tags carry a prefix installs the newest release of ITS stream -- compared
// numerically, stable versions only -- never another stream's, and never the
// repo-global "latest", which in such a repo is whichever stream released
// last.
func TestInstallShResolvesItsOwnTagStream(t *testing.T) {
	fx := newInstallFixture(t)
	s := fx.script(t, "notes/")
	dest, out, err := fx.install(t, s)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if got := installed(t, dest); got != "notes notes/v0.10.0" {
		t.Errorf("installed %q, want the newest notes/ release (notes/v0.10.0)\n%s", got, out)
	}
	// A pinned version, with or without the stream prefix.
	for _, pin := range []string{"v0.2.0", "notes/v0.2.0"} {
		dest, out, err := fx.install(t, s, "VERSION="+pin)
		if err != nil || installed(t, dest) != "notes notes/v0.2.0" {
			t.Errorf("VERSION=%s: %v %q\n%s", pin, err, installed(t, dest), out)
		}
	}
}

// TestInstallShRootCLITagsBare keeps the single-CLI shape unchanged: no prefix,
// the tag is the bare version.
func TestInstallShRootCLITagsBare(t *testing.T) {
	fx := newInstallFixture(t)
	dest, out, err := fx.install(t, fx.script(t, ""), "VERSION=v5.0.0")
	if err != nil || installed(t, dest) != "root v5.0.0" {
		t.Errorf("root CLI: %v %q\n%s", err, installed(t, dest), out)
	}
}

// TestInstallShRefusals pins what the installer must not do: install a
// release that has no build for this machine (said plainly, not as a failed
// download), install an archive whose checksum does not match, or guess when
// no release of its stream exists.
func TestInstallShRefusals(t *testing.T) {
	fx := newInstallFixture(t)
	s := fx.script(t, "notes/")

	fx.publish(t, "notes/v0.9.0", false)
	dest, out, err := fx.install(t, s, "VERSION=v0.9.0")
	if err == nil || !strings.Contains(out, "has no "+fx.os+"/"+fx.arch+" build of notes") || installed(t, dest) != "(not installed)" {
		t.Errorf("a release without this platform: %v\n%s", err, out)
	}

	asset := filepath.Join(fx.rel, "notes", "v0.2.0", fmt.Sprintf("notes_v0.2.0_%s_%s.tar.gz", fx.os, fx.arch))
	f, err := os.OpenFile(asset, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("tampered")
	_ = f.Close()
	dest, out, err = fx.install(t, s, "VERSION=v0.2.0")
	if err == nil || !strings.Contains(out, "checksum verification FAILED") || installed(t, dest) != "(not installed)" {
		t.Errorf("a tampered archive: %v\n%s", err, out)
	}

	dest, out, err = fx.install(t, fx.script(t, "absent/"))
	if err == nil || !strings.Contains(out, "no absent/v* release found") || installed(t, dest) != "(not installed)" {
		t.Errorf("a stream with no release: %v\n%s", err, out)
	}
}

// TestInstallHeaderNamesItsRegenerateCommand: a file from `volt gen install
// <dir>` must say so, or the next person runs plain `volt gen`, which skips
// install scripts in such a repo, and concludes the file is orphaned.
func TestInstallHeaderNamesItsRegenerateCommand(t *testing.T) {
	root := t.TempDir()
	vars := v
	vars.Regenerate = "volt gen install cmd/notes"
	if _, err := Generate(root, InstallScripts[0], vars, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "install.sh"))
	if !strings.Contains(string(raw), "# Regenerate:  volt gen install cmd/notes\n") {
		t.Errorf("header does not name the command:\n%s", string(raw)[:300])
	}
	root = t.TempDir()
	gen(t, root, InstallScripts[0], false)
	raw, _ = os.ReadFile(filepath.Join(root, "install.sh"))
	if !strings.Contains(string(raw), "# Regenerate:  volt gen\n") {
		t.Errorf("default header changed:\n%s", string(raw)[:300])
	}
}

// TestInstallShInstallsCompanionBinaries pins extra_binaries on the install
// side: the programs shipped beside the main binary land beside it, runnable,
// and one the release does not carry is named and skipped rather than failing
// an install of a release cut before the companion existed.
func TestInstallShInstallsCompanionBinaries(t *testing.T) {
	fx := newInstallFixture(t)
	dest, out, err := fx.install(t, fx.script(t, "notes/", "notes-sync", "notes-absent"), "VERSION=v0.2.0")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if got := installed(t, dest); got != "notes notes/v0.2.0" {
		t.Errorf("main binary = %q", got)
	}
	sync, err := exec.Command(filepath.Join(dest, "notes-sync")).CombinedOutput()
	if err != nil || strings.TrimSpace(string(sync)) != "sync notes/v0.2.0" {
		t.Errorf("companion not installed or not runnable: %v %q\n%s", err, sync, out)
	}
	if !strings.Contains(out, "NOTE: notes-absent is not in release notes/v0.2.0; skipped.") {
		t.Errorf("a companion the release lacks must be named:\n%s", out)
	}
	if !strings.Contains(out, "Installed notes notes-sync to ") {
		t.Errorf("the summary must list what was installed:\n%s", out)
	}
	// With no companions configured, none are installed even if the archive
	// happens to hold one.
	dest, _, err = fx.install(t, fx.script(t, "notes/"), "VERSION=v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "notes-sync")); err == nil {
		t.Error("an unconfigured companion was installed")
	}
}
