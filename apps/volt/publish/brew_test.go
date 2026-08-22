package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassNameFor(t *testing.T) {
	for in, want := range map[string]string{"volt": "Volt", "my-cli": "MyCli", "a_b-c": "ABC"} {
		if got := classNameFor(in); got != want {
			t.Errorf("classNameFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSumsFromChecksums(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "checksums.txt")
	content := "" +
		"aaa  volt_v1.0.0_darwin_arm64.tar.gz\n" +
		"bbb  volt_v1.0.0_linux_amd64.tar.gz\n" +
		"ccc  volt_v1.0.0_windows_amd64.zip\n" + // zip excluded: brew is tar.gz only
		"ddd  other_v1.0.0_darwin_arm64.tar.gz\n" // different binary excluded
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sums, err := SumsFromChecksums(p, "volt")
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 || sums["darwin/arm64"] != "aaa" || sums["linux/amd64"] != "bbb" {
		t.Fatalf("got %v", sums)
	}
}

func TestRenderFormula(t *testing.T) {
	got, err := RenderFormula(FormulaData{
		Binary: "volt", Desc: "the tool", Repo: "khanakia/voltkit",
		Tag: "volt/v0.1.0", BareVersion: "0.1.0",
		Sums: map[string]string{"darwin/arm64": "aaa", "linux/amd64": "bbb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"class Volt < Formula",
		`version "0.1.0"`,
		"releases/download/volt/v0.1.0/volt_v0.1.0_darwin_arm64.tar.gz",
		`sha256 "aaa"`,
		`bin.install "volt"`,
		"DO NOT EDIT",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formula missing %q:\n%s", want, got)
		}
	}
}
