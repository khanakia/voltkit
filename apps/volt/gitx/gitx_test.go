package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a git repo with one commit; returns its dir.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		mustGit(t, dir, args...)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// bareRemote wires a local bare repo as `origin` — a real remote for push
// tests, no network.
func bareRemote(t *testing.T, dir string) {
	t.Helper()
	bare := t.TempDir()
	mustGit(t, bare, "init", "-q", "--bare")
	mustGit(t, dir, "remote", "add", "origin", bare)
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestIsDirty(t *testing.T) {
	dir := initRepo(t)
	dirty, err := IsDirty(dir)
	if err != nil || dirty {
		t.Fatalf("clean repo reported dirty (err=%v)", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, _ = IsDirty(dir); !dirty {
		t.Fatal("untracked file must count as dirty")
	}
}

func TestTagLifecycleAndRace(t *testing.T) {
	dir := initRepo(t)
	bareRemote(t, dir)

	if TagExists(dir, "notes/v1.0.0") {
		t.Fatal("tag should not exist yet")
	}
	if err := CreateTag(dir, "notes/v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if !TagExists(dir, "notes/v1.0.0") {
		t.Fatal("tag should exist")
	}
	if err := PushTag(dir, "origin", "notes/v1.0.0"); err != nil {
		t.Fatal(err)
	}

	// Simulate losing the race: a second clone pushes the same tag name at a
	// DIFFERENT commit — the remote must reject it as ErrTagRejected.
	dir2 := initRepo(t)
	remoteURL := gitRemoteURL(t, dir)
	mustGit(t, dir2, "remote", "add", "origin", remoteURL)
	mustGit(t, dir2, "tag", "-a", "notes/v1.0.0", "-m", "x")
	err := PushTag(dir2, "origin", "notes/v1.0.0")
	if _, ok := err.(ErrTagRejected); !ok {
		t.Fatalf("want ErrTagRejected, got %v", err)
	}
}

func gitRemoteURL(t *testing.T, dir string) string {
	t.Helper()
	out, err := run(dir, "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Prefix listing is what keeps one CLI's auto-bump off another CLI's version.
func TestTagsWithPrefixIsolation(t *testing.T) {
	dir := initRepo(t)
	for _, tag := range []string{"notes/v1.0.0", "notes/v1.2.0", "snap/v9.9.9", "v0.1.0"} {
		mustGit(t, dir, "tag", "-a", tag, "-m", tag)
	}
	got, err := TagsWithPrefix(dir, "notes/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "notes/v1.2.0" {
		t.Fatalf("want [notes/v1.2.0 notes/v1.0.0], got %v", got)
	}
}

// Regression: an UNSTAGED modification with no committed diff — porcelain's
// " M path" line, whose leading space run()'s trim eats. The positional
// parse returned "kg/textutil/…" (first char lost) and volt ci gated the
// wrong module. Found live in volt-demo-clis, 2026-08-22.
func TestChangedFilesUnstagedOnlyKeepsFullPath(t *testing.T) {
	dir := initRepo(t)
	base, _ := Head(dir)
	sub := filepath.Join(dir, "pkg", "textutil")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-q", "-m", "add sub")
	// Unstaged edit ONLY — no commit, so the porcelain path is the sole source.
	if err := os.WriteFile(filepath.Join(sub, "x.go"), []byte("package x // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := ChangedFiles(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f == "pkg/textutil/x.go" {
			found = true
		}
		if strings.Contains(f, "kg/textutil") && !strings.Contains(f, "pkg/") {
			t.Fatalf("truncated path resurfaced: %q", f)
		}
	}
	if !found {
		t.Fatalf("want full path pkg/textutil/x.go, got %v", files)
	}
}

func TestChangedFilesIncludesUncommitted(t *testing.T) {
	dir := initRepo(t)
	base, _ := Head(dir)
	for f, c := range map[string]string{"f.txt": "changed", "brand-new.txt": "n"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-q", "-m", "edit")
	files, err := ChangedFiles(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f] = true
	}
	if !seen["f.txt"] || !seen["brand-new.txt"] {
		t.Fatalf("want committed AND uncommitted changes, got %v", files)
	}
}
