// hash.go — the canonical hash and the reference-keyed comparison behind
// `skills check` and `skills version`.
//
// Two principles from the spec, both settled after real alternatives were
// rejected (decision 11):
//
//   - freshness is COMPUTED, never stored: a stored marker (VERSION file,
//     frontmatter stamp, timestamp) is a proxy maintained by discipline and
//     lies after one missed write; a recomputed comparison is the content
//     itself, maintained by math.
//   - the comparison is keyed on the REFERENCE's file list: installed
//     directories accumulate junk (.DS_Store, ._*, harness metadata) that
//     must never affect the verdict, and a missing reference file must.
package skillcmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TreeHash computes the canonical hash of a directory: sorted walk, hidden
// entries (dotfiles) excluded at every depth, sha256 over each relative
// path + NUL + content. Recomputable from any copy of the same content —
// repo, bundle, cache — and therefore comparable across all of them.
func TreeHash(dir string) (string, error) {
	files, err := visibleFiles(dir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(content)
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// visibleFiles lists a tree's non-hidden files, relative, sorted — the one
// definition of "what counts" shared by hashing and comparison.
func visibleFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && path != dir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, err
}

// CheckResult reports one installed-copy comparison.
type CheckResult struct {
	Current bool     `json:"current"`
	Stale   []string `json:"stale,omitempty"`  // reference files missing or differing
	Extras  []string `json:"extras,omitempty"` // installed-side files outside the reference — informational only
}

// CompareDirs verifies an installed copy against the reference (the
// binary's copy of the same skill). The verdict is decided ONLY by the
// reference's files: each must exist installed-side with identical bytes.
// Extra installed files are reported but never affect Current — junk cannot
// cry wolf, and a user's stray note is not staleness.
func CompareDirs(refDir, installedDir string) (CheckResult, error) {
	res := CheckResult{Current: true}
	refFiles, err := visibleFiles(refDir)
	if err != nil {
		return res, fmt.Errorf("reading the reference copy: %w", err)
	}
	refSet := map[string]bool{}
	for _, rel := range refFiles {
		refSet[rel] = true
		want, err := os.ReadFile(filepath.Join(refDir, rel))
		if err != nil {
			return res, err
		}
		got, err := os.ReadFile(filepath.Join(installedDir, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(want, got) {
			res.Current = false
			res.Stale = append(res.Stale, rel)
		}
	}
	installedFiles, err := visibleFiles(installedDir)
	if err != nil {
		return res, fmt.Errorf("reading the installed copy: %w", err)
	}
	for _, rel := range installedFiles {
		if !refSet[rel] {
			res.Extras = append(res.Extras, rel)
		}
	}
	return res, nil
}
