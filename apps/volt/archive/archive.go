// Package archive produces release artifacts: tar.gz / zip archives and the
// checksums.txt manifest.
//
// Pure stdlib on purpose (archive/tar, archive/zip, crypto/sha256): this is
// the 80% of goreleaser volt implements itself; anything heavier (signing,
// SBOM) is delegated, never rebuilt (spec, "Do not rewrite all of
// goreleaser").
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
	"sort"
	"strings"
)

// Entry is one file to place in an archive. Name is the path INSIDE the
// archive (flat names like "notes" or "README.md"); Path is where the bytes
// live on disk; Mode carries the executable bit for the binary.
type Entry struct {
	Name string
	Path string
	Mode os.FileMode
}

// WriteTarGz writes entries to a .tar.gz at dest. Directories are not
// supported deliberately — release archives are a flat, small, predictable
// set of files, and flatness is what keeps `tar xz -C /usr/local/bin volt`
// one-liners in the install scripts working.
func WriteTarGz(dest string, entries []Entry) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	// The explicit f.Close() below is the checked one; this defer only
	// covers early-error returns, where the write already failed.
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := addTarEntry(tw, e); err != nil {
			return fmt.Errorf("%s: add %s: %w", dest, e.Name, err)
		}
	}
	// Close order matters: tar flushes into gzip flushes into the file, and
	// each Close can surface a write error the writes deferred.
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func addTarEntry(tw *tar.Writer, e Entry) error {
	src, err := os.Open(e.Path)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }() // read-only; a failed close loses nothing
	st, err := src.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name: e.Name,
		Mode: int64(e.Mode.Perm()),
		Size: st.Size(),
		// ModTime deliberately left zero: a reproducible archive should not
		// change bytes because it was rebuilt a minute later.
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, src)
	return err
}

// WriteZip writes entries to a .zip at dest — the windows archive format.
func WriteZip(dest string, entries []Entry) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	// See WriteTarGz: the checked close is the explicit one at the end.
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	for _, e := range entries {
		if err := addZipEntry(zw, e); err != nil {
			return fmt.Errorf("%s: add %s: %w", dest, e.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

func addZipEntry(zw *zip.Writer, e Entry) error {
	src, err := os.Open(e.Path)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }() // read-only; a failed close loses nothing
	hdr := &zip.FileHeader{Name: e.Name, Method: zip.Deflate}
	hdr.SetMode(e.Mode)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

// ChecksumsFileName is the manifest name — a public contract: the install
// scripts and volt-action both fetch this exact name to verify downloads.
const ChecksumsFileName = "checksums.txt"

// WriteChecksums writes "sha256hex␠␠basename" lines for every file, sorted by
// name, in the `sha256sum` format so `sha256sum -c` verifies it directly.
// Sorting makes the manifest deterministic — two builds of the same inputs
// must produce byte-identical checksums.txt.
func WriteChecksums(dir string, files []string) (string, error) {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	var b strings.Builder
	for _, name := range sorted {
		sum, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		// Two spaces is sha256sum's text-mode separator; one space would
		// make `sha256sum -c` treat the file as binary-mode mismatch.
		fmt.Fprintf(&b, "%s  %s\n", sum, name)
	}
	dest := filepath.Join(dir, ChecksumsFileName)
	if err := os.WriteFile(dest, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// TarGzDir archives a whole directory under wrapper/ inside the tar —
// the shape the skills bundle uses (skills/<content>), matching what
// skillcmd's extractor strips. Hidden entries (dotfiles) are excluded: OS
// junk must never ship in a published bundle.
func TarGzDir(dest, srcDir, wrapper string) error {
	var entries []Entry
	err := filepathWalk(srcDir, func(rel string, mode os.FileMode) {
		entries = append(entries, Entry{
			Name: wrapper + "/" + rel,
			Path: filepath.Join(srcDir, filepath.FromSlash(rel)),
			Mode: mode,
		})
	})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("%s: empty directory — an empty bundle would serve nothing and look successful", srcDir)
	}
	return WriteTarGz(dest, entries)
}

// filepathWalk lists non-hidden files under root (relative, sorted by walk
// order), calling fn for each — the bundle's visibility rule in one place.
func filepathWalk(root string, fn func(rel string, mode os.FileMode)) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && path != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		fn(filepath.ToSlash(rel), info.Mode())
		return nil
	})
}
