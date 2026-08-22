// Package gitx wraps the git operations the release pipeline needs.
//
// Thin by design: git is a hard dependency (spec, "Hard assumptions") and its
// CLI is the stable interface — no go-git, no libgit2. Every function takes
// the repo directory explicitly so nothing depends on process cwd.
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// MaxTagsPerPush is GitHub's silent limit: a push carrying MORE than three
// tags triggers zero workflow runs — no error, no skipped-run entry (hit for
// real in ubgo/buildinfo, 2026-08-20). Every tag push volt makes is batched
// at this size.
const MaxTagsPerPush = 3

// run executes git with args in dir, returning trimmed stdout.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// IsDirty reports uncommitted changes (staged, unstaged, or untracked).
// Releases refuse dirty trees: the tag must name a commit that contains
// exactly what was built.
func IsDirty(dir string) (bool, error) {
	out, err := run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// Head returns the current commit hash.
func Head(dir string) (string, error) { return run(dir, "rev-parse", "HEAD") }

// TagExists reports whether the tag exists locally.
func TagExists(dir, tag string) bool {
	_, err := run(dir, "rev-parse", "-q", "--verify", "refs/tags/"+tag)
	return err == nil
}

// CreateTag creates an annotated tag at HEAD. Annotated (not lightweight)
// because the Go module proxy and `git describe` both prefer them.
func CreateTag(dir, tag string) error {
	_, err := run(dir, "tag", "-a", tag, "-m", tag)
	return err
}

// ErrTagRejected reports a push refused because the ref already exists on the
// remote — the "someone else won the race" signal the release loop keys on
// (ADR-R10: the atomic tag push is the only lock).
type ErrTagRejected struct{ Tag string }

func (e ErrTagRejected) Error() string {
	return fmt.Sprintf("tag %s already exists on the remote", e.Tag)
}

// PushTag pushes one tag, translating an already-exists rejection into
// ErrTagRejected so callers can distinguish "lost the race" from "network
// down". One tag per call keeps every push far under MaxTagsPerPush.
func PushTag(dir, remote, tag string) error {
	_, err := run(dir, "push", remote, "refs/tags/"+tag)
	if err == nil {
		return nil
	}
	msg := err.Error()
	// Both spellings appear across git versions/servers.
	if strings.Contains(msg, "already exists") || strings.Contains(msg, "cannot lock ref") {
		return ErrTagRejected{Tag: tag}
	}
	return err
}

// FetchTags refreshes local tags from the remote — required before computing
// a next version, and again after losing a push race.
func FetchTags(dir, remote string) error {
	_, err := run(dir, "fetch", remote, "--tags", "--force")
	return err
}

// TagsWithPrefix lists local tags matching prefix+"*", newest version first.
// Used by auto-bump, which must read the newest tag FOR ONE DIRECTORY'S
// PREFIX — the repo-wide newest tag would bump another CLI's number.
func TagsWithPrefix(dir, prefix string) ([]string, error) {
	out, err := run(dir, "tag", "-l", prefix+"*", "--sort=-v:refname")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// TagCommit returns the commit a tag points at (peeled through the
// annotation), for verifying a reservation before spending minutes building.
func TagCommit(dir, tag string) (string, error) {
	return run(dir, "rev-list", "-n1", "refs/tags/"+tag)
}

// ChangedFiles lists paths changed between base and HEAD plus anything
// uncommitted — the input to volt ci's changed-module detection.
func ChangedFiles(dir, base string) ([]string, error) {
	committed, err := run(dir, "diff", "--name-only", base, "HEAD")
	if err != nil {
		return nil, err
	}
	uncommitted, err := run(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	add := func(f string) {
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	for _, f := range strings.Split(committed, "\n") {
		add(f)
	}
	for _, l := range strings.Split(uncommitted, "\n") {
		// Porcelain v1 is "XY path" — but NOT parseable positionally here:
		// run() trims the command output, which eats the leading space of a
		// " M path" first line and shifts every index (found the hard way:
		// a changed module came back as "kg/textutil"). Parse by cutting at
		// the first space after the trimmed status code instead.
		t := strings.TrimSpace(l)
		if _, path, ok := strings.Cut(t, " "); ok {
			path = strings.TrimSpace(path)
			// Renames read "old -> new"; the NEW path is the one that
			// exists to be gated.
			if _, renamed, ok := strings.Cut(path, " -> "); ok {
				path = renamed
			}
			add(path)
		}
	}
	return files, nil
}

// MergeBase returns the merge base of HEAD and ref, or "" when it cannot be
// computed (shallow clone, unborn branch) — callers treat "" as "run
// everything", the safe fallback.
func MergeBase(dir, ref string) string {
	out, err := run(dir, "merge-base", "HEAD", ref)
	if err != nil {
		return ""
	}
	return out
}
