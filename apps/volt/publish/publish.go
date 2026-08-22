// Package publish owns the "make it public" half of a release: the GitHub
// Release and, later, other channels.
//
// GitHub sits behind the Publisher interface (spec, "Hard assumptions") so
// GitLab/Codeberg become drivers rather than rewrites — and so the release
// orchestrator is testable against a fake without network. Repo identity and
// URL shapes moved to the forge seam (apps/volt/forge) — this package is the
// GitHub publish DRIVER that forge.GitHub hands out; per the forge ADR, new
// forge-specific code goes there, not here.
package publish

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Publisher is the publish target. Exactly one real implementation exists
// (GitHub via the gh CLI); tests use a fake.
type Publisher interface {
	// ReleaseExists reports whether a release for tag already exists.
	ReleaseExists(tag string) bool
	// CreateOrUpdate publishes idempotently: edit when the release exists,
	// create otherwise — the only recovery route once a tag is public, since
	// tags cannot be deleted and re-pushed (the proxy caches them forever).
	CreateOrUpdate(tag, title, notesFile string, assets []string) error
	// FetchBody re-reads the published release body — the "verify the
	// artifact, not the exit code" half of publishing.
	FetchBody(tag string) (string, error)
	// FetchAssetNames lists the published release's asset names.
	FetchAssetNames(tag string) ([]string, error)
}

// GH publishes via the `gh` CLI in a repo directory. gh over go-github:
// auth, host resolution and retries are gh's problem, and gh is already a
// hard dependency of the workflows this replaces.
type GH struct {
	Dir string // repo directory gh runs in (it infers the GitHub repo from the remote)
}

func (g GH) run(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = g.Dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (g GH) ReleaseExists(tag string) bool {
	_, err := g.run("release", "view", tag, "--json", "tagName")
	return err == nil
}

func (g GH) CreateOrUpdate(tag, title, notesFile string, assets []string) error {
	// Notes travel by FILE, never by argument interpolation: changelog prose
	// contains backticks, and a shell-interpolated body is how ubgo/buildinfo
	// published releases with executed-command output spliced in (2026-08-20).
	if g.ReleaseExists(tag) {
		if _, err := g.run("release", "edit", tag, "--title", title, "--notes-file", notesFile); err != nil {
			return err
		}
		if len(assets) > 0 {
			args := append([]string{"release", "upload", tag, "--clobber"}, assets...)
			if _, err := g.run(args...); err != nil {
				return err
			}
		}
		return nil
	}
	args := []string{"release", "create", tag, "--title", title, "--notes-file", notesFile, "--verify-tag"}
	args = append(args, assets...)
	_, err := g.run(args...)
	return err
}

func (g GH) FetchBody(tag string) (string, error) {
	return g.run("release", "view", tag, "--json", "body", "--jq", ".body")
}

func (g GH) FetchAssetNames(tag string) ([]string, error) {
	out, err := g.run("release", "view", tag, "--json", "assets", "--jq", ".assets[].name")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Verify re-reads the published release and diffs it against what was meant
// to be published: the notes body and every asset name. Returns a list of
// discrepancies — empty means verified. A missing asset or mangled body is
// how both real incidents (spliced release bodies; partial uploads) would
// have been caught on the day.
func Verify(p Publisher, tag, wantNotes string, wantAssets []string) []string {
	var problems []string
	body, err := p.FetchBody(tag)
	if err != nil {
		return []string{fmt.Sprintf("could not re-fetch release %s: %v", tag, err)}
	}
	if normalize(body) != normalize(wantNotes) {
		problems = append(problems, fmt.Sprintf("release %s body differs from the notes that were uploaded", tag))
	}
	published, err := p.FetchAssetNames(tag)
	if err != nil {
		return append(problems, fmt.Sprintf("could not list release %s assets: %v", tag, err))
	}
	have := map[string]bool{}
	for _, a := range published {
		have[a] = true
	}
	for _, want := range wantAssets {
		if !have[filepath.Base(want)] {
			problems = append(problems, fmt.Sprintf("release %s is missing asset %s", tag, filepath.Base(want)))
		}
	}
	return problems
}

// normalize strips CR and trailing whitespace so a GitHub round-trip (which
// rewrites line endings) does not read as a discrepancy.
func normalize(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// WriteNotesFile writes notes to a temp file for --notes-file and returns its
// path; caller removes it.
func WriteNotesFile(notes string) (string, error) {
	f, err := os.CreateTemp("", "volt-notes-*.md")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(notes); err != nil {
		_ = f.Close() // the write error is the one worth reporting
		return "", err
	}
	return f.Name(), f.Close()
}
