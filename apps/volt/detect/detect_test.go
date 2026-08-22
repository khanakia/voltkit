package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func scaffold(t *testing.T, pkg string) string {
	t.Helper()
	dir := t.TempDir()
	gomod := "module example.test/x\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package " + pkg + "\n"
	if pkg == "main" {
		src += "func main() {}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDirCLI(t *testing.T) {
	k, err := Dir(scaffold(t, "main"))
	if err != nil {
		t.Fatal(err)
	}
	if k != KindCLI {
		t.Fatalf("got %q", k)
	}
}

func TestDirLibrary(t *testing.T) {
	k, err := Dir(scaffold(t, "mylib"))
	if err != nil {
		t.Fatal(err)
	}
	if k != KindLibrary {
		t.Fatalf("got %q", k)
	}
}

// No Go package at all → hard error, never a guess (ADR-R06).
func TestDirNoPackageErrors(t *testing.T) {
	if _, err := Dir(t.TempDir()); err == nil {
		t.Fatal("want error for empty dir, got nil")
	}
}

// The aws-sdk-go-v2 shape: go.mod at the root, every package in a subdir.
// That root is a LIBRARY — its release is a bare tag consumers resolve.
func TestDirModuleRootWithoutRootPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/sdk\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "aws"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aws", "aws.go"), []byte("package aws\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k != KindLibrary {
		t.Fatalf("module root without a root package must be a library, got %q", k)
	}
}
