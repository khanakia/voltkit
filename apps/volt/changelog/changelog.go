// Package changelog extracts one version's section from a Keep-a-Changelog
// style CHANGELOG.md — the source of release notes.
//
// Soft convention with a fallback (spec, "Soft conventions"): a missing file
// or missing section yields a generated one-liner, never an error and never
// an empty release body.
package changelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the conventional changelog location inside the released
// directory (per-directory in a monorepo, so each CLI owns its history).
const FileName = "CHANGELOG.md"

// Notes returns the release-notes body for version (bare, without the "v" —
// pass "1.4.0" for tag v1.4.0). Resolution order:
//
//  1. the `## [1.4.0]` section of dir/CHANGELOG.md, if non-blank
//  2. a generated one-liner naming the tag and linking the changelog
//
// The blank check matters: a section heading followed only by whitespace must
// fall through — a run of blank lines is not release notes (hit for real in
// ubgo/buildinfo's awk version).
func Notes(dir, tag, bareVersion, repo string) string {
	if body := section(filepath.Join(dir, FileName), bareVersion); strings.TrimSpace(body) != "" {
		return strings.TrimSpace(body) + "\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Release `%s`.\n", tag)
	if repo != "" {
		fmt.Fprintf(&b, "\nSee [CHANGELOG.md](https://github.com/%s/blob/main/%s) for details.\n", repo, FileName)
	}
	return b.String()
}

// section returns the text between `## [ver]` and the next `## [` heading.
func section(path, ver string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	marker := "## [" + ver + "]"
	var out []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, marker):
			in = true
		case in && strings.HasPrefix(line, "## ["):
			return strings.Join(out, "\n")
		case in:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
