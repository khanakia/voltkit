package skillcmd

// edge_final_test.go — the last reachable branches, each named for the line
// it pins. What remains uncovered after this file is individually justified
// in TESTING.md-style comments at the bottom.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// httpGet's NewRequest error branch: a URL with a control byte fails request
// construction before any network I/O.
func TestHttpGetInvalidURL(t *testing.T) {
	if _, err := httpGet("http://\x7f"); err == nil {
		t.Fatal("invalid URL must fail at request construction")
	}
}

// ensure/refresh surfacing cacheDir failure (CacheRoot empty + no HOME):
// the error must name the env override — through BOTH entry points.
func TestEnsureAndRefreshSurfaceCacheDirFailure(t *testing.T) {
	o := Options{Binary: "demo", Repo: "r", Version: "v1.0.0"}
	o.applyDefaults()
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if _, err := os.UserCacheDir(); err == nil {
		t.Skip("platform resolves a cache dir without HOME — branch unreachable here")
	}
	if _, err := ensure(o); err == nil || !strings.Contains(err.Error(), o.Env) {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := refresh(o); err == nil || !strings.Contains(err.Error(), o.Env) {
		t.Fatalf("refresh: %v", err)
	}
}

// refresh routed through the COMMAND with an unremovable cache — the
// command-level error branch (the function-level one has its own test).
func TestRefreshCommandSurfacesFailure(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "x"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)
	if _, err := ensure(o); err != nil {
		t.Fatal(err)
	}
	base, _ := cacheDir(o)
	denyRead(t, base)
	if _, err := run(t, o, "refresh"); err == nil {
		t.Fatal("refresh command must surface the failure")
	}
}

// untar into an unwritable destination: extraction fails cleanly.
func TestUntarUnwritableDest(t *testing.T) {
	b, _ := makeBundle(t, "v1", map[string]string{"a/SKILL.md": "x"})
	dest := t.TempDir()
	sub := filepath.Join(dest, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	denyRead(t, sub)
	if err := untarStrip1(bytesReader(b), filepath.Join(sub, "deeper")); err == nil {
		t.Fatal("unwritable destination must error")
	}
}

// emitSkill's trailing-newline branch: a reference file without a final
// newline still yields well-formed banner-delimited output.
func TestGetFullNoTrailingNewline(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	write(t, root, "lore-core/references/raw.md", "no trailing newline")
	out, err := run(t, o, "get", "lore-core", "--full")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "newline---") || !strings.Contains(out, "no trailing newline\n") {
		t.Fatalf("missing synthesized newline:\n%s", out)
	}
}

// get (non-list) with an unreadable member skill: the load error surfaces
// through get too, not only list.
func TestGetSurfacesLoadErrors(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	denyRead(t, filepath.Join(root, "lore-search"))
	if _, err := run(t, o, "get", "lore-core"); err == nil {
		t.Fatal("load error must surface even when the requested skill is readable")
	}
}

// emitJSON propagating an emitSkill failure (--json --full + unreadable ref).
func TestGetJSONFullUnreadableReference(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	denyRead(t, filepath.Join(root, "lore-core", "references", "commands.md"))
	if _, err := run(t, o, "get", "lore-core", "--json", "--full"); err == nil {
		t.Fatal("emitJSON must propagate read failures")
	}
}

// LoadAll on a nonexistent root — the ReadDir error branch directly.
func TestLoadAllMissingRoot(t *testing.T) {
	if _, err := LoadAll(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root must error")
	}
}

// CompareDirs: per-FILE read failure on the reference side (directory
// listable, one file denied) — distinct from the unwalkable-dir case.
func TestCompareDirsUnreadableRefFile(t *testing.T) {
	ref, inst := installPair(t)
	denyRead(t, filepath.Join(ref, "references", "cmds.md"))
	if _, err := CompareDirs(ref, inst); err == nil {
		t.Fatal("unreadable reference FILE must error")
	}
}

// TreeHash: same per-file distinction.
func TestTreeHashUnreadableFileInListableDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.md", "x")
	write(t, dir, "b.md", "y")
	denyRead(t, filepath.Join(dir, "b.md"))
	if _, err := TreeHash(dir); err == nil {
		t.Fatal("unreadable file must error the hash")
	}
}

// ---- Coverage ledger: what remains BELOW 100% and why that is correct ----
//
//   githubFetcher.Fetch success half — requires the real github.com host
//     (hardcoded by design; the URL-composition logic is covered via
//     urlFetcher against httptest, the error half via the live-404 test).
//   cleanOthers' ReadDir-error return — documented best-effort: cleanup
//     failure must never fail serving, so the branch is a bare return.
//   Deep OS-catastrophe branches (Getwd failing, Close failing on a file
//     just written, MkdirAll racing) — not constructible without fault
//     injection that would distort the production code for the test's sake.
