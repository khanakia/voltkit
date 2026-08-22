package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/khanakia/voltkit/apps/volt/detect"
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
