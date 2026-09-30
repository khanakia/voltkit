package gobuild

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldWithHelpers is scaffoldCLI plus companion programs and a library
// package in subdirectories of the same module.
func scaffoldWithHelpers(t *testing.T) string {
	t.Helper()
	dir := scaffoldCLI(t)
	for rel, body := range map[string]string{
		"plugins/notes-sync/main.go": "package main\n\nfunc main() { println(\"sync\") }\n",
		"plugins/notes-gc/main.go":   "package main\n\nfunc main() { println(\"gc\") }\n",
		"other/notes-sync/main.go":   "package main\n\nfunc main() {}\n",
		"lib/lib.go":                 "package lib\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// tarEntries lists name -> mode for a .tar.gz.
func tarEntries(t *testing.T, path string) map[string]os.FileMode {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]os.FileMode{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = h.FileInfo().Mode()
	}
	return out
}

// TestExtraBinariesShipInTheSameArchive pins extra_binaries: a tool's
// companion programs are built for the platform and packed beside the main
// binary, executable, so one download holds a set that cannot be mismatched.
// Only the main binary's stamp is verified; a helper without the version
// symbol is not a warning.
func TestExtraBinariesShipInTheSameArchive(t *testing.T) {
	if hostOS() == "windows" {
		t.Skip("reads a .tar.gz; windows archives are .zip")
	}
	dir := scaffoldWithHelpers(t)
	dist := t.TempDir()
	cfg := hostCfg(dir)
	cfg.ExtraBinaries = []string{"plugins/notes-sync", "./plugins/notes-gc/"}

	res, err := Run(Options{Dir: dir, Version: "v0.1.0", DistDir: dist}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("a helper without the stamp symbol must not warn: %v", res.Warnings)
	}
	got := tarEntries(t, filepath.Join(dist, res.Assets[0]))
	for _, name := range []string{filepath.Base(dir), "notes-sync", "notes-gc"} {
		mode, ok := got[name]
		if !ok {
			t.Errorf("archive lacks %s: %v", name, got)
			continue
		}
		if mode&0o111 == 0 {
			t.Errorf("%s is not executable in the archive: %v", name, mode)
		}
	}
	if len(got) != 3 {
		t.Errorf("archive entries = %v, want exactly the three binaries", got)
	}
}

// TestExtraBinariesRefusals: a name that would collide in the archive, and a
// directory that is not a program, fail the build rather than publish an
// archive missing or overwriting a binary.
func TestExtraBinariesRefusals(t *testing.T) {
	dir := scaffoldWithHelpers(t)
	for name, tc := range map[string]struct {
		extras []string
		want   string
	}{
		"two extras with one name":   {[]string{"plugins/notes-sync", "other/notes-sync"}, "already plugins/notes-sync"},
		"an extra named as the main": {[]string{"."}, "already the main binary"},
		"a library directory":        {[]string{"lib"}, "not a program"},
		"a directory that is absent": {[]string{"nope"}, "extra_binaries nope"},
	} {
		cfg := hostCfg(dir)
		cfg.ExtraBinaries = tc.extras
		_, err := Run(Options{Dir: dir, Version: "v0.1.0", DistDir: t.TempDir()}, cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}
