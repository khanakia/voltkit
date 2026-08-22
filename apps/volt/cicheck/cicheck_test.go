package cicheck

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

// multi-module fixture: root is NOT a module; a/ and b/ are.
func fixture(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "a/go.mod", "module example.test/a\n\ngo 1.22\n")
	write(t, root, "a/a.go", "package a\n")
	write(t, root, "b/go.mod", "module example.test/b\n\ngo 1.22\n")
	write(t, root, "b/b.go", "package b\n")
	write(t, root, "README.md", "docs\n")
	return root
}

func TestModulesFindsAll(t *testing.T) {
	mods, err := Modules(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(mods, ",") != "a,b" {
		t.Fatalf("got %v", mods)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestChangedNarrowsToTouchedModule(t *testing.T) {
	root := fixture(t)
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "t@t")
	git(t, root, "config", "user.name", "t")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init")
	base := headOf(t, root)

	write(t, root, "a/a.go", "package a\n\nvar X = 1\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "touch a")

	mods, err := Changed(root, base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(mods, ",") != "a" {
		t.Fatalf("want just module a, got %v", mods)
	}
}

// A change outside every module (root README) must fall back to ALL modules
// — the safe direction.
func TestChangedOutsideModulesRunsEverything(t *testing.T) {
	root := fixture(t)
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "t@t")
	git(t, root, "config", "user.name", "t")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init")
	base := headOf(t, root)

	write(t, root, "README.md", "changed docs\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "docs")

	mods, _ := Changed(root, base)
	if strings.Join(mods, ",") != "a,b" {
		t.Fatalf("out-of-module change must run everything, got %v", mods)
	}
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// Gate collects EVERY problem instead of stopping at the first.
func TestGateCollectsAllProblems(t *testing.T) {
	root := t.TempDir()
	// Unformatted AND vet-failing AND test-failing module.
	write(t, root, "go.mod", "module example.test/bad\n\ngo 1.22\n")
	write(t, root, "bad.go", "package bad\n\nimport \"fmt\"\n\nfunc F() {\nfmt.Printf(\"%d\", \"not a number\")\n}\n")
	write(t, root, "bad_test.go", "package bad\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { t.Fatal(\"boom\") }\n")
	problems := Gate(root, ".", io.Discard, false)
	if len(problems) < 3 {
		t.Fatalf("want gofmt+vet+test all reported, got %d: %v", len(problems), problems)
	}
}

func TestGateCleanModule(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.test/ok\n\ngo 1.22\n")
	write(t, root, "ok.go", "package ok\n")
	if problems := Gate(root, ".", io.Discard, false); len(problems) != 0 {
		t.Fatalf("clean module must pass: %v", problems)
	}
}

// --fix repairs formatting so the gate passes on what remains.
func TestGateFixRepairsFormatting(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.test/fixme\n\ngo 1.22\n")
	write(t, root, "bad.go", "package fixme\n\nfunc   F()   int   { return 1 }\n")
	if problems := Gate(root, ".", io.Discard, true); len(problems) != 0 {
		t.Fatalf("--fix must leave a clean gate: %v", problems)
	}
	raw, _ := os.ReadFile(root + "/bad.go")
	if strings.Contains(string(raw), "func   F") {
		t.Fatal("file was not actually reformatted")
	}
}
