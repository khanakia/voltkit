package relname

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/khanakia/voltkit/apps/volt/detect"
)

func TestComposeShapes(t *testing.T) {
	cases := []struct {
		kind   detect.Kind
		dir    string
		binary string
		want   string
	}{
		{detect.KindCLI, ".", "notes", "v1.4.0"},              // root: bare
		{detect.KindLibrary, ".", "", "v1.4.0"},               // root library: bare
		{detect.KindLibrary, "version", "", "version/v1.4.0"}, // library: full path
		{detect.KindLibrary, "contrib/foo", "", "contrib/foo/v1.4.0"},
		{detect.KindCLI, "cmd/notes", "", "notes/v1.4.0"},                // CLI: dir base name
		{detect.KindCLI, "cmd/notes", "supernotes", "supernotes/v1.4.0"}, // CLI: binary override
	}
	for _, c := range cases {
		got, err := Compose(c.kind, c.dir, c.binary, "v1.4.0")
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if got != c.want {
			t.Errorf("Compose(%v,%q,%q) = %q, want %q", c.kind, c.dir, c.binary, got, c.want)
		}
	}
}

func TestComposeRejectsBadVersion(t *testing.T) {
	for _, bad := range []string{"1.4.0", "v1.4", "v1", "latest", "v1.4.0 "} {
		if _, err := Compose(detect.KindCLI, ".", "x", bad); err == nil {
			t.Errorf("version %q must be rejected", bad)
		}
	}
}

// repo builds a fixture: version/ (library), cmd/notes (CLI), apps/tool with
// binary: notes override candidate.
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
	write("version/lib.go", "package version\n")
	write("cmd/notes/main.go", "package main\n\nfunc main() {}\n")
	return root
}

func TestResolveLibraryByExactPath(t *testing.T) {
	r, err := Resolve(repo(t), "version/v0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir != "version" || r.Kind != detect.KindLibrary || r.Version != "v0.3.0" {
		t.Fatalf("got %+v", r)
	}
}

func TestResolveCLIByBinaryName(t *testing.T) {
	r, err := Resolve(repo(t), "notes/v1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir != "cmd/notes" || r.Kind != detect.KindCLI {
		t.Fatalf("got %+v", r)
	}
}

func TestResolveBareTagIsRoot(t *testing.T) {
	root := repo(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(root, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir != "." || r.Kind != detect.KindCLI {
		t.Fatalf("got %+v", r)
	}
}

func TestResolveNoMatchIsHardError(t *testing.T) {
	if _, err := Resolve(repo(t), "ghost/v1.0.0"); err == nil {
		t.Fatal("unknown name must be a hard error, never a guess")
	}
}

func TestResolveAmbiguityIsHardError(t *testing.T) {
	root := repo(t)
	// A second main package that ALSO claims "notes" via .volt.yml.
	if err := os.MkdirAll(filepath.Join(root, "apps/tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, c := range map[string]string{"apps/tool/main.go": "package main\nfunc main(){}\n", "apps/tool/.volt.yml": "binary: notes\n"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Resolve(root, "notes/v1.4.0")
	if err == nil {
		t.Fatal("two claimants must be a hard error naming the fix")
	}
}
