// Package upgrade implements `volt upgrade`: re-apply template evolution to
// an already-scaffolded project (spec D13; the downstream half of template
// propagation — `volt gen`'s hash guard covers volt-owned files, this covers
// the scaffolded files the project owns and has edited).
//
// The mechanic is the cruft/copier model:
//
//	old  = the template at the RECORDED commit, re-rendered with the
//	       RECORDED inputs (the generation stamp makes this reproducible)
//	new  = the template at the target ref, rendered with the same inputs
//	diff = what the template changed
//
// That diff is applied to the project THREE-WAY, so user edits survive:
// clean when only one side touched a region, conflict markers when both did.
// The merge engine is `git merge-file` — git is already a hard dependency,
// and a hand-rolled diff3 is exactly the kind of subtle code that ships
// silent corruption.
package upgrade

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/khanakia/voltkit/apps/volt/scaffold"
)

// Stamp is the typed form of .volt.yml's `generated:` block — everything
// needed to reproduce the original render.
type Stamp struct {
	By       string // "volt v0.4.0" — the generator
	Template string // "owner/repo//templates/<variant>"
	Ref      string // the ref scaffolding used (informational; Commit is authoritative)
	Commit   string // the RESOLVED commit — what "old" is re-rendered from
	At       string // RFC3339 generation time, reproduced into the old render
	Name     string
	Module   string
	Variant  string
}

// ParseStamp converts the opaque `generated:` map voltcfg carries into a
// Stamp, failing loudly on anything missing — a partial stamp cannot
// reproduce the old render, and guessing would corrupt the diff.
func ParseStamp(m map[string]any) (Stamp, error) {
	var st Stamp
	if len(m) == 0 {
		return st, fmt.Errorf("no `generated:` stamp in .volt.yml — this project was not scaffolded by volt new (or the stamp was removed)")
	}
	// yaml.v3 resolves RFC3339-looking scalars into time.Time inside `any`
	// maps — the stamp's `at:` field arrives as a time.Time, not a string
	// (hit live on the first real upgrade). Convert it back to the exact
	// RFC3339 text volt new wrote, because the old render must reproduce
	// the original bytes.
	str := func(key string) string { return stampString(m[key]) }
	st.By, st.Template, st.Ref, st.Commit, st.At = str("by"), str("template"), str("ref"), str("commit"), str("at")
	if inputs, ok := m["inputs"].(map[string]any); ok {
		get := func(key string) string { return stampString(inputs[key]) }
		st.Name, st.Module, st.Variant = get("name"), get("module"), get("variant")
	}
	var missing []string
	for field, v := range map[string]string{
		"template": st.Template, "commit": st.Commit, "at": st.At,
		"inputs.name": st.Name, "inputs.module": st.Module, "inputs.variant": st.Variant,
	} {
		if v == "" {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return st, fmt.Errorf("generated: stamp is missing %s — cannot reproduce the original render", strings.Join(missing, ", "))
	}
	return st, nil
}

// stampString renders one stamp scalar back to the text volt new wrote.
// Strings pass through; time.Time (yaml.v3's parse of the `at:` field)
// formats back to RFC3339 — the exact shape volt new writes, which the old
// render must reproduce byte-for-byte.
func stampString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// Repo splits "owner/repo//templates/<variant>" into its repo half.
func (st Stamp) Repo() (string, error) {
	repo, _, ok := strings.Cut(st.Template, "//")
	if !ok || strings.Count(repo, "/") != 1 {
		return "", fmt.Errorf("stamp template %q: want owner/repo//templates/<variant>", st.Template)
	}
	return repo, nil
}

// Outcome classifies what happened to one file.
type Outcome string

const (
	// Updated — the template changed and the project took the change
	// cleanly (including merges where user edits and template edits touched
	// different regions).
	Updated Outcome = "updated"
	// Conflicted — user edits and template edits overlap; the file now
	// contains git-style conflict markers for the human to resolve.
	Conflicted Outcome = "conflict"
	// Deleted — the template removed the file and the project had not
	// edited it, so it was removed locally too.
	Deleted Outcome = "deleted"
	// KeptDeleted — the project deleted a file the template still changes;
	// the deletion is respected, never resurrected.
	KeptDeleted Outcome = "kept-deleted"
	// KeptEdited — the template deleted a file the project edited; the
	// user's version is kept.
	KeptEdited Outcome = "kept-edited"
)

// FileResult is one file's outcome.
type FileResult struct {
	Path    string
	Outcome Outcome
}

// Report is what an upgrade did.
type Report struct {
	Results   []FileResult
	Conflicts int
}

// Run renders oldRoot and newRoot with their respective vars and applies the
// template diff to projectDir. Pure local operation — fetching happened
// before this, which is what makes every merge scenario unit-testable.
func Run(projectDir, oldRoot, newRoot, variant string, oldVars, newVars scaffold.Vars, log io.Writer) (Report, error) {
	var rep Report
	if log == nil {
		log = io.Discard
	}
	tmp, err := os.MkdirTemp("", "volt-upgrade-*")
	if err != nil {
		return rep, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	oldRender := filepath.Join(tmp, "old")
	newRender := filepath.Join(tmp, "new")
	if _, err := scaffold.Generate(oldRoot, variant, oldRender, oldVars); err != nil {
		return rep, fmt.Errorf("re-rendering the old template: %w", err)
	}
	if _, err := scaffold.Generate(newRoot, variant, newRender, newVars); err != nil {
		return rep, fmt.Errorf("rendering the new template: %w", err)
	}

	for _, rel := range unionFiles(oldRender, newRender) {
		res, err := applyOne(projectDir, oldRender, newRender, tmp, rel)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", rel, err)
		}
		if res.Outcome == "" {
			continue // template unchanged for this file, or project already current
		}
		rep.Results = append(rep.Results, res)
		if res.Outcome == Conflicted {
			rep.Conflicts++
		}
		_, _ = fmt.Fprintf(log, "%-12s %s\n", res.Outcome, rel)
	}
	return rep, nil
}

// applyOne decides and applies one file's upgrade. The empty Outcome means
// "nothing to do" and is filtered by the caller.
func applyOne(project, oldRender, newRender, tmp, rel string) (FileResult, error) {
	res := FileResult{Path: rel}
	oldB, oldExists := readIf(filepath.Join(oldRender, rel))
	newB, newExists := readIf(filepath.Join(newRender, rel))
	curB, curExists := readIf(filepath.Join(project, rel))

	// Template unchanged → the project keeps whatever it has.
	if oldExists == newExists && bytes.Equal(oldB, newB) {
		return res, nil
	}

	switch {
	case !newExists: // template deleted the file
		if !curExists {
			return res, nil // already gone
		}
		if bytes.Equal(curB, oldB) {
			if err := os.Remove(filepath.Join(project, rel)); err != nil {
				return res, err
			}
			res.Outcome = Deleted
			return res, nil
		}
		res.Outcome = KeptEdited // user edited it; a deletion must not eat their work
		return res, nil

	case !curExists:
		if !oldExists {
			// Brand-new template file, absent locally → create it.
			if err := writeFile(filepath.Join(project, rel), newB); err != nil {
				return res, err
			}
			res.Outcome = Updated
			return res, nil
		}
		// The PROJECT deleted a file the template still changes: the
		// deletion is an edit, and edits are never overridden silently.
		res.Outcome = KeptDeleted
		return res, nil

	case bytes.Equal(curB, newB):
		return res, nil // already current (e.g. a re-run after a partial upgrade)

	case bytes.Equal(curB, oldB):
		// Never edited by the user → clean fast-forward, no merge needed.
		if err := writeFile(filepath.Join(project, rel), newB); err != nil {
			return res, err
		}
		res.Outcome = Updated
		return res, nil

	default:
		// User edits AND template changes: three-way merge. An absent old
		// side (template-new file the user also created) merges against an
		// empty base — both creations surface as a conflict, which is
		// honest: there is no common ancestor to reason from.
		merged, clean, err := mergeFile(tmp, curB, oldB, newB, rel)
		if err != nil {
			return res, err
		}
		if err := writeFile(filepath.Join(project, rel), merged); err != nil {
			return res, err
		}
		if clean {
			res.Outcome = Updated
		} else {
			res.Outcome = Conflicted
		}
		return res, nil
	}
}

// mergeFile three-way merges via `git merge-file`. git is already a hard
// dependency, and a hand-rolled diff3 is subtle enough to ship silent
// corruption — exactly what a merge engine must never do. Labels make the
// conflict markers self-explanatory in the file. Returns the merged bytes
// and whether the merge was clean.
func mergeFile(tmp string, cur, oldB, newB []byte, rel string) ([]byte, bool, error) {
	// git merge-file works on files and merges INTO the first one.
	work := filepath.Join(tmp, "merge-"+strings.ReplaceAll(rel, "/", "_"))
	base := work + ".base"
	theirs := work + ".new"
	for path, content := range map[string][]byte{work: cur, base: oldB, theirs: newB} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return nil, false, err
		}
	}
	cmd := exec.Command("git", "merge-file",
		"-L", "yours ("+rel+")", "-L", "old template", "-L", "new template",
		work, base, theirs)
	err := cmd.Run()
	clean := err == nil
	if err != nil {
		// Exit status 1..127 = number of conflicts (expected); >127 or a
		// start failure is a real error.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() < 0 || ee.ExitCode() > 127 {
			return nil, false, fmt.Errorf("git merge-file: %w", err)
		}
	}
	merged, rerr := os.ReadFile(work)
	return merged, clean, rerr
}

// unionFiles lists every file present in either render, repo-relative,
// sorted — the iteration set for the upgrade.
func unionFiles(a, b string) []string {
	seen := map[string]bool{}
	collect := func(root string) {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			seen[filepath.ToSlash(rel)] = true
			return nil
		})
	}
	collect(a)
	collect(b)
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// readIf reads a file, reporting whether it exists; any other error is
// treated as non-existence deliberately — the caller's bytes.Equal logic
// then routes it to the safest outcome (keep, never overwrite).
func readIf(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

// writeFile writes with parent-directory creation.
func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
