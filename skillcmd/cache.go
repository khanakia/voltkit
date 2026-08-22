// cache.go — the ensure→clean half of the content model: fetch this
// version's bundle once, verify it, extract atomically, delete every other
// version. See "Content source — always dynamic, self-syncing" in the spec.
package skillcmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// completeMarker guards against serving a half-extracted cache: it is
// written LAST, so a crash mid-extract leaves a directory ensure treats as
// absent and re-fetches. Absence of the marker == absence of the cache.
const completeMarker = ".complete"

// cacheDir is <os-cache>/<binary>/skills — platform-correct via
// os.UserCacheDir (macOS ~/Library/Caches, Linux ~/.cache, Windows
// %LocalAppData%). A failing UserCacheDir is a hard error pointing at the
// env override — never a guessed fallback path.
func cacheDir(o Options) (string, error) {
	root := o.CacheRoot
	if root == "" {
		var err error
		root, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("no OS cache directory available (%v) — set %s to a skills directory instead", err, o.Env)
		}
	}
	return filepath.Join(root, o.Binary, "skills"), nil
}

// ensure returns the ready-to-serve directory for THIS binary's version,
// fetching and extracting when absent, then cleaning other versions.
func ensure(o Options) (string, error) {
	base, err := cacheDir(o)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, o.Version)
	if _, err := os.Stat(filepath.Join(dir, completeMarker)); err == nil {
		return dir, nil // warm cache — the normal, fully offline path
	}

	if err := fetchInto(o, dir); err != nil {
		return "", err
	}
	cleanOthers(base, o.Version)
	return dir, nil
}

// fetchInto downloads, verifies and extracts the bundle for o.Version.
// Extraction is staged into a temp sibling and renamed into place so a
// crash can never leave a directory that looks complete.
func fetchInto(o Options, dir string) error {
	bundle, sums, err := o.Fetcher.Fetch(o.Repo, o.Tag, o.Version)
	if err != nil {
		return fmt.Errorf("fetching skills for %s %s: %w\n(cache: %s — offline? the first use per version needs the network once)",
			o.Binary, o.Version, err, dir)
	}

	// Verify against the release's checksums.txt BEFORE extraction —
	// the same never-install-unverified rule as everywhere else.
	if err := verifyBundle(bundle, sums, bundleAssetName(o.Version)); err != nil {
		return err
	}

	staging := dir + ".tmp"
	_ = os.RemoveAll(staging)
	if err := untarStrip1(bytes.NewReader(bundle), staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("extracting skills bundle: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, completeMarker), []byte(o.Version+"\n"), 0o644); err != nil {
		return err
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	return os.Rename(staging, dir)
}

// bundleAssetName is the published asset's name for a version.
//
// PAIRED CONSTANT: volt's release side builds this exact name in
// apps/volt/release (buildSkillsBundle) — the two cannot share a constant
// because volt imports nothing from kit modules (ADR-R08). Changing this
// shape means changing BOTH sites, or every fetch 404s.
func bundleAssetName(version string) string {
	return "skills_" + version + ".tar.gz"
}

// verifyBundle checks the tarball's sha256 against the checksums.txt line
// for its asset name. A missing line is as fatal as a mismatch: an
// unverifiable download is an unverified download.
func verifyBundle(bundle, sums []byte, asset string) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s — refusing the unverifiable bundle", asset)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(bundle))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s — refusing the corrupted bundle (want %s, got %s)", asset, want, got)
	}
	return nil
}

// cleanOthers deletes every version directory except keep. Stale docs have
// no reason to exist: the binary has exactly one version. Errors are
// ignored — cleanup is best-effort, correctness never depends on it.
func cleanOthers(base, keep string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
}

// refresh deletes THIS version's cache and re-ensures — the explicit
// recovery command for a re-published bundle. Never automatic.
func refresh(o Options) (string, error) {
	base, err := cacheDir(o)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(filepath.Join(base, o.Version)); err != nil {
		return "", err
	}
	return ensure(o)
}

// untarStrip1 extracts a tar.gz, dropping the single top-level wrapper
// directory when one exists (release bundles wrap content in skills/).
func untarStrip1(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.ToSlash(hdr.Name)
		parts := strings.SplitN(name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(parts[1]))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}
