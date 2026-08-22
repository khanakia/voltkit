package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// Round-trip: what goes into the tar.gz must come back out byte-identical,
// with the executable bit intact (inverse-direction check).
func TestTarGzRoundTrip(t *testing.T) {
	dir := t.TempDir()
	bin := writeTemp(t, dir, "notes", "#!binary", 0o755)
	readme := writeTemp(t, dir, "README.md", "docs", 0o644)
	dest := filepath.Join(dir, "out.tar.gz")

	err := WriteTarGz(dest, []Entry{
		{Name: "notes", Path: bin, Mode: 0o755},
		{Name: "README.md", Path: readme, Mode: 0o644},
	})
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	modes := map[string]int64{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[hdr.Name] = string(b)
		modes[hdr.Name] = hdr.Mode
	}
	if got["notes"] != "#!binary" || got["README.md"] != "docs" {
		t.Fatalf("content mismatch: %v", got)
	}
	if modes["notes"]&0o111 == 0 {
		t.Error("binary lost its executable bit")
	}
}

func TestZipRoundTrip(t *testing.T) {
	dir := t.TempDir()
	bin := writeTemp(t, dir, "notes.exe", "MZbinary", 0o755)
	dest := filepath.Join(dir, "out.zip")
	if err := WriteZip(dest, []Entry{{Name: "notes.exe", Path: bin, Mode: 0o755}}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) != 1 || zr.File[0].Name != "notes.exe" {
		t.Fatalf("zip contents: %v", zr.File)
	}
	rc, _ := zr.File[0].Open()
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "MZbinary" {
		t.Error("zip content mismatch")
	}
}

// checksums.txt is verified by `sha256sum -c` in install scripts — pin the
// exact line format and the determinism guarantee.
func TestChecksumsFormatAndDeterminism(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "b.tar.gz", "bbb", 0o644)
	writeTemp(t, dir, "a.tar.gz", "aaa", 0o644)

	dest, err := WriteChecksums(dir, []string{"b.tar.gz", "a.tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dest)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", raw)
	}
	// Sorted regardless of input order.
	if !strings.HasSuffix(lines[0], "  a.tar.gz") || !strings.HasSuffix(lines[1], "  b.tar.gz") {
		t.Fatalf("not sorted or wrong separator: %q", lines)
	}
	wantA := fmt.Sprintf("%x", sha256.Sum256([]byte("aaa")))
	if !strings.HasPrefix(lines[0], wantA) {
		t.Fatalf("bad sha256 for a.tar.gz: %q", lines[0])
	}
}

// An empty skills bundle would serve nothing while looking successful —
// TarGzDir must refuse it (skeleton honesty).
func TestTarGzDirRefusesEmptyAndExcludesHidden(t *testing.T) {
	empty := t.TempDir()
	if err := TarGzDir(filepath.Join(t.TempDir(), "e.tar.gz"), empty, "skills"); err == nil {
		t.Fatal("empty directory must refuse")
	}
	src := t.TempDir()
	writeTemp(t, src, "SKILL.md", "content", 0o644)
	writeTemp(t, src, ".DS_Store", "junk", 0o644)
	dest := filepath.Join(t.TempDir(), "ok.tar.gz")
	if err := TarGzDir(dest, src, "skills"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, _ := gzip.NewReader(f)
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[hdr.Name] = true
	}
	if !names["skills/SKILL.md"] || names["skills/.DS_Store"] {
		t.Fatalf("wrapper shape or hidden-exclusion wrong: %v", names)
	}
}

// TarGzDir: missing source directory errors; a file vanishing mid-walk is
// not constructible portably, but an unreadable subdirectory is.
func TestTarGzDirUnreadableSubdir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	src := t.TempDir()
	writeTemp(t, src, "SKILL.md", "x", 0o644)
	sub := filepath.Join(src, "refs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, sub, "r.md", "y", 0o644)
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
	if err := TarGzDir(filepath.Join(t.TempDir(), "x.tar.gz"), src, "skills"); err == nil {
		t.Fatal("unreadable subdir must error, never ship a silently smaller bundle")
	}
	if err := TarGzDir(filepath.Join(t.TempDir(), "y.tar.gz"), filepath.Join(src, "missing"), "skills"); err == nil {
		t.Fatal("missing source must error")
	}
}
