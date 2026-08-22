package streams

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/khanakia/voltkit/apps/volt/detect"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// monorepo fixture: root module (AWS shape), cmd/notes CLI, lib/ library
// module — three streams.
func repo(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.test/repo\n\ngo 1.22\n")
	write(t, root, "cmd/notes/main.go", "package main\n\nfunc main() {}\n")
	write(t, root, "lib/go.mod", "module example.test/repo/lib\n\ngo 1.22\n")
	write(t, root, "lib/lib.go", "package lib\n")
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "t@t")
	git(t, root, "config", "user.name", "t")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init")
	return root
}

func TestDiscoverStreamsAndSuggestions(t *testing.T) {
	root := repo(t)
	git(t, root, "tag", "-a", "notes/v0.2.0", "-m", "x")
	git(t, root, "tag", "-a", "lib/v0.1.0", "-m", "x")
	// One commit AFTER the tags, touching only cmd/notes.
	write(t, root, "cmd/notes/main.go", "package main\n\nfunc main() { println(1) }\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "touch notes")

	streams, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	byDir := map[string]Stream{}
	for _, s := range streams {
		byDir[s.Dir] = s
	}
	if len(streams) != 3 {
		t.Fatalf("want 3 streams (root, cmd/notes, lib), got %+v", streams)
	}
	// Root: AWS-shaped module, unreleased.
	if r := byDir["."]; r.Kind != detect.KindLibrary || r.LastVersion != "" {
		t.Errorf("root: %+v", r)
	}
	// notes: released, one commit ahead, patch suggested.
	n := byDir["cmd/notes"]
	if n.Prefix != "notes/" || n.LastVersion != "v0.2.0" || n.CommitsAhead != 1 || n.Suggested != "v0.2.1" {
		t.Errorf("notes: %+v", n)
	}
	// lib: released, untouched since — no suggestion.
	l := byDir["lib"]
	if l.LastVersion != "v0.1.0" || l.CommitsAhead != 0 || l.Suggested != "" {
		t.Errorf("lib: %+v", l)
	}
}

func TestBumpLevels(t *testing.T) {
	cases := []struct{ cur, level, want string }{
		{"v1.2.3", "patch", "v1.2.4"},
		{"v1.2.3", "minor", "v1.3.0"},
		{"v1.2.3", "major", "v2.0.0"},
	}
	for _, c := range cases {
		got, err := Bump(c.cur, c.level)
		if err != nil || got != c.want {
			t.Errorf("Bump(%s,%s) = %s,%v want %s", c.cur, c.level, got, err, c.want)
		}
	}
	if _, err := Bump("v1.2.3", "huge"); err == nil {
		t.Error("bad level must error")
	}
	if _, err := Bump("1.2.3", "patch"); err == nil {
		t.Error("non-v version must error")
	}
}

// Pre-releases and other streams' tags must not pollute version resolution.
func TestNewestVersionExactShape(t *testing.T) {
	root := repo(t)
	for _, tag := range []string{"notes/v0.1.0", "notes/v0.2.0-rc.1", "notes-extra/v9.9.9"} {
		git(t, root, "tag", "-a", tag, "-m", "x")
	}
	if got := newestVersion(root, "notes/"); got != "v0.1.0" {
		t.Fatalf("got %q — rc or foreign-stream tag leaked in", got)
	}
}

func TestNextUnreleasedStartsAtFirstVersion(t *testing.T) {
	root := repo(t)
	next, err := Next(root, "cmd/notes", "patch")
	if err != nil || next != FirstVersion {
		t.Fatalf("got %q, %v", next, err)
	}
	git(t, root, "tag", "-a", "notes/v0.3.0", "-m", "x")
	next, err = Next(root, "./cmd/notes", "minor") // raw ./ form must work too
	if err != nil || next != "v0.4.0" {
		t.Fatalf("got %q, %v", next, err)
	}
}

// Regression: the command passes root=".", and filepath.Rel against a
// relative root fails — which silently dropped every CLI stream from
// `volt status` (caught live on volt-demo-clis, 2026-08-22).
func TestDiscoverFromRelativeRoot(t *testing.T) {
	root := repo(t)
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	streams, err := Discover(".")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range streams {
		if s.Dir == "cmd/notes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CLI stream missing with relative root: %+v", streams)
	}
}

// `internal: true` hides a directory AND its subdirectories from status —
// intent volt cannot detect, so it is the one config field for it.
func TestDiscoverSkipsInternal(t *testing.T) {
	root := repo(t)
	write(t, root, "lib/.volt.yml", "internal: true\n")
	write(t, root, "lib/cmd/helper/main.go", "package main\n\nfunc main() {}\n")
	streams, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range streams {
		if s.Dir == "lib" || s.Dir == "lib/cmd/helper" {
			t.Fatalf("internal dir leaked into status: %+v", s)
		}
	}
}
