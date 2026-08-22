package skillcmd

// edge_ioerr_test.go — the I/O-error branches: unreadable files and dirs,
// resolve failures inside every command, and the checksums-fetch failure.
// Permission-based cases skip as root (root reads anything).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// denyRead removes read permission and restores it at cleanup, skipping the
// test where permissions cannot deny (root).
func denyRead(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root — permission denial cannot be simulated")
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// Every serving command funnels through resolveDir — one unreadable
// override dir must fail list, get, path, version and check identically.
func TestAllCommandsSurfaceResolveErrors(t *testing.T) {
	o := Options{Binary: "demo", Repo: "r", Version: "v1.0.0", CacheRoot: t.TempDir()}
	o.applyDefaults()
	t.Setenv(o.Env, filepath.Join(t.TempDir(), "missing"))
	for _, args := range [][]string{
		{"list"}, {"get", "x"}, {"get", "--all"}, {"path"}, {"version"},
		{"check", t.TempDir()},
	} {
		if _, err := run(t, o, args...); err == nil {
			t.Fatalf("%v must surface the resolve error", args)
		}
	}
}

// LoadAll on an unreadable skill dir: the walk error propagates — never a
// silently shorter list.
func TestLoadAllUnreadableSkillDir(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/SKILL.md", "---\nname: a\n---\n")
	write(t, root, "a/references/x.md", "ref")
	denyRead(t, filepath.Join(root, "a", "references"))
	if _, err := LoadAll(root); err == nil {
		t.Fatal("unreadable subdir must error")
	}
}

// skillFromMD read failure (SKILL.md exists in the listing race but is
// unreadable) → error, not a ghost skill.
func TestLoadAllUnreadableSkillMD(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/SKILL.md", "---\nname: a\n---\n")
	denyRead(t, filepath.Join(root, "a", "SKILL.md"))
	if _, err := LoadAll(root); err == nil {
		t.Fatal("unreadable SKILL.md must error")
	}
}

// TreeHash and CompareDirs on unreadable content: errors named, never a
// wrong verdict.
func TestHashAndCompareUnreadable(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "SKILL.md", "x")
	denyRead(t, filepath.Join(dir, "SKILL.md"))
	if _, err := TreeHash(dir); err == nil {
		t.Fatal("TreeHash must error on unreadable file")
	}
	ref := t.TempDir()
	write(t, ref, "SKILL.md", "x")
	denyRead(t, filepath.Join(ref, "SKILL.md"))
	if _, err := CompareDirs(ref, t.TempDir()); err == nil {
		t.Fatal("CompareDirs must error when the REFERENCE is unreadable")
	}
	// Unreadable installed side during the extras scan: also an error.
	ref2, inst := t.TempDir(), t.TempDir()
	write(t, ref2, "SKILL.md", "x")
	write(t, inst, "SKILL.md", "x")
	write(t, inst, "sub/extra.md", "e")
	denyRead(t, filepath.Join(inst, "sub"))
	if _, err := CompareDirs(ref2, inst); err == nil {
		t.Fatal("CompareDirs must error when the installed tree is unwalkable")
	}
}

// get --full with an unreadable reference file: the error propagates.
func TestGetFullUnreadableReference(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	denyRead(t, filepath.Join(root, "lore-core", "references", "commands.md"))
	if _, err := run(t, o, "get", "lore-core", "--full"); err == nil {
		t.Fatal("unreadable reference must error under --full")
	}
	// Without --full the reference is never read — must still succeed.
	if _, err := run(t, o, "get", "lore-core"); err != nil {
		t.Fatalf("non-full get must not touch references: %v", err)
	}
}

// version command with an unhashable tree.
func TestVersionUnreadableTree(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	denyRead(t, filepath.Join(root, "quickref.md"))
	if _, err := run(t, o, "version"); err == nil {
		t.Fatal("version must surface hash errors")
	}
}

// ensure with an unwritable cache root: the MkdirAll/staging failure
// surfaces; nothing half-created is served.
func TestEnsureUnwritableCacheRoot(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "x"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)
	denyRead(t, o.CacheRoot) // 0o000: unwritable AND unreadable
	if _, err := ensure(o); err == nil {
		t.Fatal("unwritable cache root must error")
	}
}

// refresh when the version dir cannot be removed (parent unwritable).
func TestRefreshUnremovableCache(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "x"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)
	if _, err := ensure(o); err != nil {
		t.Fatal(err)
	}
	base, _ := cacheDir(o)
	denyRead(t, base)
	if _, err := refresh(o); err == nil {
		t.Fatal("unremovable cache must error")
	}
}

// The checksums request failing AFTER a successful bundle download — the
// second half of the fetch pair (urlFetcher mirrors production shape).
func TestFetchChecksumsFailureSurfaces(t *testing.T) {
	bundle, _ := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "x"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, bundleAssetName("v1.0.0")) {
			_, _ = w.Write(bundle)
			return
		}
		http.NotFound(w, r) // checksums.txt missing
	}))
	defer srv.Close()
	o := opts(t, urlFetcher{tpl: srv.URL + "/dl/{tag}/{asset}"})
	_, err := ensure(o)
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("missing checksums must fail naming the file: %v", err)
	}
}

// path <name> resolve error branch: env points somewhere unreadable AFTER
// the dir resolves (skills listable, one entry unreadable at load).
func TestPathUnknownName(t *testing.T) {
	o := liveOpts(t)
	if _, err := run(t, o, "path", "ghost"); err == nil {
		t.Fatal("unknown name in path must error")
	}
}

// list --json on a valid tree with an unreadable member: error, not a
// shorter JSON array (silent narrowing ban).
func TestListJSONUnreadableMember(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	denyRead(t, filepath.Join(root, "lore-search"))
	if _, err := run(t, o, "list", "--json"); err == nil {
		t.Fatal("unreadable member must error the whole listing")
	}
}
