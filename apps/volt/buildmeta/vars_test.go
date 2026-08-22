package buildmeta

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	v := Vars{Version: "v1.4.0", ShortCommit: "abc1234", OS: "linux"}
	got, err := v.Render("{{.Version}}+{{.ShortCommit}}.{{.OS}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.4.0+abc1234.linux" {
		t.Fatalf("got %q", got)
	}
}

// A typo in a template variable must fail, not silently render empty — an
// empty -X value would produce an unstamped binary from a green build.
func TestRenderUnknownFieldFails(t *testing.T) {
	if _, err := (Vars{}).Render("{{.Verion}}"); err == nil {
		t.Fatal("want error for unknown field, got nil")
	}
}

func TestFromGitOutsideRepo(t *testing.T) {
	v := FromGit(t.TempDir(), "v0.0.1")
	if v.Commit != "unknown" || v.ShortCommit != "unknown" {
		t.Fatalf("outside a repo git facts must be 'unknown', got %+v", v)
	}
	if v.Version != "v0.0.1" || v.BuildTime == "" {
		t.Fatalf("version/buildtime not set: %+v", v)
	}
	if !strings.HasSuffix(v.BuildTime, "Z") {
		t.Fatalf("BuildTime must be UTC RFC3339, got %q", v.BuildTime)
	}
}

func TestFromGitInsideRepo(t *testing.T) {
	// The kit repo itself may have no commits yet (fresh clone of the
	// boilerplate), in which case git facts legitimately resolve to
	// "unknown" — so only assert on shape, not on a specific hash.
	v := FromGit(".", "v0.0.1")
	if v.Commit != "unknown" && len(v.ShortCommit) != 7 {
		t.Fatalf("ShortCommit should be 7 chars when a commit exists: %+v", v)
	}
}
