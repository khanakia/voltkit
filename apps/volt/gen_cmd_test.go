package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanakia/voltkit/apps/volt/detect"
	"github.com/khanakia/voltkit/apps/volt/forge"
)

// tagPrefix must mirror relname.Compose exactly — rule one's shapes. The
// root case is the one that shipped wrong once: a single-CLI repo tags
// BARE, so its skills wiring must carry an empty prefix, not "<binary>/".
func TestTagPrefixMirrorsRuleOne(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sub := filepath.Join(root, "cmd", "notes")
	lib := filepath.Join(root, "pkg", "textutil")
	for _, d := range []string{sub, lib} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		cwd    string
		kind   detect.Kind
		binary string
		want   string
	}{
		{"root CLI tags bare", root, detect.KindCLI, "volt-demo-cli", ""},
		{"root library tags bare", root, detect.KindLibrary, "", ""},
		{"CLI subdir tags by binary", sub, detect.KindCLI, "notes", "notes/"},
		{"CLI subdir defaults binary to dir base", sub, detect.KindCLI, "", "notes/"},
		{"library subdir tags by path", lib, detect.KindLibrary, "", "pkg/textutil/"},
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.Chdir(c.cwd); err != nil {
				t.Fatal(err)
			}
			got, err := tagPrefix(c.kind, c.binary)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("tagPrefix(%v, %q) = %q, want %q", c.kind, c.binary, got, c.want)
			}
		})
	}
}

// TestGenInstallForSubdirCLI pins `volt gen install <dir>`: a repo whose root
// is not a single CLI gets install scripts for the CLI in dir, carrying that
// CLI's tag prefix (rule one, as release composes it) and a header naming the
// command that regenerates them. A library directory, a directory outside the
// repository, and a missing <dir> are refused rather than guessed at.
func TestGenInstallForSubdirCLI(t *testing.T) {
	root := t.TempDir()
	for _, c := range [][]string{{"git", "-C", root, "init", "-q"}} {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", c, err, out)
		}
	}
	files := map[string]string{
		"go.mod":                   "module example.com/m\n\ngo 1.22\n",
		"lib.go":                   "package m\n",
		"cmd/notes/main.go":        "package main\n\nfunc main() {}\n",
		"pkg/textutil/textutil.go": "package textutil\n",
	}
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cmd := newGenCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := genInstall(cmd, forge.GitHub{}, "o/n", "./cmd/notes", false); err != nil {
		t.Fatalf("gen install: %v\n%s", err, out.String())
	}
	sh, err := os.ReadFile(filepath.Join(root, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`TAG_PREFIX="notes/"`,
		`BINARY="notes"`,
		"https://github.com/o/n.git",
		"https://github.com/o/n/releases/download/${TAG}",
		"# Regenerate:  volt gen install cmd/notes\n",
	} {
		if !strings.Contains(string(sh), want) {
			t.Errorf("install.sh lacks %q", want)
		}
	}
	ps, err := os.ReadFile(filepath.Join(root, "install.ps1"))
	if err != nil || !strings.Contains(string(ps), `$TagPrefix  = "notes/"`) {
		t.Errorf("install.ps1 = %v, lacks the tag prefix", err)
	}
	// Regenerating unchanged is quiet and allowed.
	out.Reset()
	if err := genInstall(cmd, forge.GitHub{}, "o/n", "cmd/notes", false); err != nil || !strings.Contains(out.String(), "unchanged install.sh") {
		t.Errorf("regenerate = %v\n%s", err, out.String())
	}

	if err := genInstall(cmd, forge.GitHub{}, "o/n", "pkg/textutil", false); err == nil || !strings.Contains(err.Error(), "not a CLI") {
		t.Errorf("a library dir must be refused: %v", err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := genInstall(cmd, forge.GitHub{}, "o/n", outside, false); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("a dir outside the repository must be refused: %v", err)
	}
}
