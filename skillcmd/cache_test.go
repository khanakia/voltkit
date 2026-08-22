package skillcmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFetcher serves an in-memory bundle and counts calls — every cache
// scenario tests without a network.
type fakeFetcher struct {
	bundle []byte
	sums   []byte
	err    error
	calls  int
}

func (f *fakeFetcher) Fetch(repo, tag, version string) ([]byte, []byte, error) {
	f.calls++
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.bundle, f.sums, nil
}

// makeBundle tars a map of rel→content under a top-level skills/ wrapper
// (exactly the shape volt release publishes) and returns bundle + a correct
// checksums.txt for it.
func makeBundle(t *testing.T, version string, files map[string]string) (bundle, sums []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for rel, content := range files {
		hdr := &tar.Header{Name: "skills/" + rel, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	line := fmt.Sprintf("%x  %s\n", sha256.Sum256(b), bundleAssetName(version))
	return b, []byte(line)
}

func opts(t *testing.T, f Fetcher) Options {
	t.Helper()
	o := Options{
		Binary: "demo", Repo: "khanakia/demo", Version: "v1.0.0",
		Fetcher: f, CacheRoot: t.TempDir(),
	}
	o.applyDefaults()
	return o
}

func TestEnsureFetchesOnceThenServesOffline(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "---\nname: core\n---\nbody\n"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)

	dir, err := ensure(o)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "core", "SKILL.md"))
	if err != nil || !strings.Contains(string(raw), "body") {
		t.Fatalf("extracted content wrong: %v %q", err, raw)
	}
	// Second ensure: warm cache, ZERO network.
	if _, err := ensure(o); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("warm cache must not refetch: %d calls", f.calls)
	}
}

func TestEnsureCorruptedBundleRefusedAndNothingCached(t *testing.T) {
	b, _ := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "x"})
	// checksums.txt claims a different hash → corruption.
	f := &fakeFetcher{bundle: b, sums: []byte(strings.Repeat("0", 64) + "  " + bundleAssetName("v1.0.0") + "\n")}
	o := opts(t, f)
	_, err := ensure(o)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corruption must refuse: %v", err)
	}
	// And leave NO servable cache behind.
	base, _ := cacheDir(o)
	if _, err := os.Stat(filepath.Join(base, o.Version, completeMarker)); !os.IsNotExist(err) {
		t.Fatal("a refused bundle must not leave a complete cache")
	}
}

func TestEnsureMissingChecksumLineRefused(t *testing.T) {
	b, _ := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "x"})
	f := &fakeFetcher{bundle: b, sums: []byte("aaaa  something_else.tar.gz\n")}
	_, err := ensure(opts(t, f))
	if err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("an unverifiable bundle is an unverified bundle: %v", err)
	}
}

// A half-extracted cache (no .complete marker) must be treated as absent.
func TestEnsureHalfExtractRefetches(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "fresh\n"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)
	base, _ := cacheDir(o)
	// Simulate a crash mid-extract: files present, marker absent.
	write(t, filepath.Join(base, o.Version), "core/SKILL.md", "torn write")
	dir, err := ensure(o)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "core", "SKILL.md"))
	if string(raw) != "fresh\n" {
		t.Fatalf("half-extract must refetch, got %q", raw)
	}
	if f.calls != 1 {
		t.Fatalf("expected exactly one fetch, got %d", f.calls)
	}
}

// Upgrading the binary fetches ITS version and deletes every other one.
func TestEnsureCleansOtherVersions(t *testing.T) {
	b, s := makeBundle(t, "v2.0.0", map[string]string{"core/SKILL.md": "v2"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)
	base, _ := cacheDir(o)
	// Leftovers from two older binaries.
	write(t, filepath.Join(base, "v1.0.0"), completeMarker, "v1.0.0")
	write(t, filepath.Join(base, "v1.5.0"), completeMarker, "v1.5.0")
	o.Version = "v2.0.0"
	o.Tag = "v2.0.0"
	if _, err := ensure(o); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 || entries[0].Name() != "v2.0.0" {
		t.Fatalf("old versions must be cleaned: %v", entries)
	}
}

func TestOfflineColdCacheErrorNamesCacheAndCause(t *testing.T) {
	f := &fakeFetcher{err: errors.New("dial tcp: no route to host")}
	o := opts(t, f)
	_, err := ensure(o)
	if err == nil || !strings.Contains(err.Error(), "no route to host") || !strings.Contains(err.Error(), o.Version) {
		t.Fatalf("cold-cache failure must name cause and version: %v", err)
	}
}

func TestRefreshRefetchesSameVersion(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "first\n"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)
	if _, err := ensure(o); err != nil {
		t.Fatal(err)
	}
	// Publisher re-uploaded a corrected bundle for the SAME version.
	f.bundle, f.sums = makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "corrected\n"})
	dir, err := refresh(o)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "core", "SKILL.md"))
	if string(raw) != "corrected\n" {
		t.Fatalf("refresh must replace the cache: %q", raw)
	}
	if f.calls != 2 {
		t.Fatalf("want exactly 2 fetches (ensure + refresh), got %d", f.calls)
	}
}

func TestCacheDirFailureNamesEnvOverride(t *testing.T) {
	o := Options{Binary: "demo", Repo: "r", Version: "v1.0.0"}
	o.applyDefaults()
	o.CacheRoot = "" // force the real UserCacheDir path lookup...
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if _, err := cacheDir(o); err != nil {
		if !strings.Contains(err.Error(), o.Env) {
			t.Fatalf("cache failure must point at the env override: %v", err)
		}
		return
	}
	t.Skip("platform still resolves a cache dir without HOME — nothing to assert")
}
