// Package buildmeta collects the template variables available everywhere in
// volt — ldflags values, asset names, toolchain templates.
//
// One vocabulary on purpose: .Version, .Commit, .BuildTime etc. mean the same
// thing in `ldflags.vars`, in `toolchain.cc`, and in any future generator, so
// a user learns them once (see "Template variables" in
// docsi/RELEASE_PIPELINE_SPEC.md).
package buildmeta

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"text/template"
	"time"
)

// Vars is the full template vocabulary. Fields are resolved once per build
// invocation (FromGit) and then rendered into many strings; keeping them in
// one struct means a new variable is added in exactly one place.
type Vars struct {
	Version     string // the version being built, e.g. v1.4.0 (or a snapshot string)
	Tag         string // the full tag, e.g. notes/v1.4.0; equals Version for root releases
	Commit      string // full commit hash
	ShortCommit string // first 7 characters of Commit
	BuildTime   string // UTC RFC3339, resolved once so every platform stamps identically
	Branch      string // current branch, "" in detached HEAD
	OS          string // set per platform by the builder
	Arch        string // set per platform by the builder
	ZigTarget   string // zig target triple, set per platform when cgo is on
	Env         string // deploy environment, "" outside volt deploy
	Dir         string // the directory being built, repo-relative
	Binary      string // the binary name
}

// unknown is the fallback for git facts when the directory is not a git
// checkout (e.g. building from an exported tarball). A visible "unknown" in
// `volt version` output is honest; failing the build over provenance is not.
const unknown = "unknown"

// FromGit resolves the git-derived fields for dir. Never fails: outside a
// repo every git field is "unknown", matching what the Taskfile build does
// today. BuildTime is resolved here — once — so a five-platform build carries
// one timestamp, not five.
func FromGit(dir, version string) Vars {
	v := Vars{
		Version:   version,
		Tag:       version,
		Commit:    gitOut(dir, "rev-parse", "HEAD"),
		Branch:    gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"),
		BuildTime: time.Now().UTC().Format(time.RFC3339),
	}
	v.ShortCommit = v.Commit
	if len(v.Commit) > 7 && v.Commit != unknown {
		v.ShortCommit = v.Commit[:7]
	}
	if v.Branch == "HEAD" { // detached HEAD reports the literal string "HEAD"
		v.Branch = ""
	}
	return v
}

// gitOut runs one git query and returns its trimmed output, or "unknown".
func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return unknown
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return unknown
	}
	return s
}

// Render expands a template string ("{{.Version}}") against v.
// missingkey=error: a typo like {{.Verion}} must fail the build, not stamp an
// empty string — the silent-empty case is exactly the class of defect the
// spec's "no silent narrowing" rule exists to prevent.
func (v Vars) Render(tmpl string) (string, error) {
	t, err := template.New("").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", tmpl, err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, v); err != nil {
		return "", fmt.Errorf("template %q: %w", tmpl, err)
	}
	return b.String(), nil
}
