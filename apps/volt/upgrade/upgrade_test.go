package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanakia/voltkit/apps/volt/scaffold"
)

// ---- fixtures -----------------------------------------------------------

// templateRoot builds a local template repo root with the given _base files
// (name → content; ".tmpl" suffix in the name means rendered).
func templateRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "volt-templates.yml", "version: 1\ndefault: minimal\nvariants:\n  minimal:\n    description: d\n")
	for name, content := range files {
		write(t, root, filepath.Join("templates/_base", name), content)
	}
	// every variant needs its directory to exist, even when empty of overlays
	if err := os.MkdirAll(filepath.Join(root, "templates/minimal"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

var (
	oldVars = scaffold.Vars{Name: "mytool", Module: "example.test/mytool", Variant: "minimal",
		Ref: "v0.1.0", TemplateRepo: "r", TemplateCommit: "aaa", VoltVersion: "v0.1.0", At: "t1"}
	newVars = scaffold.Vars{Name: "mytool", Module: "example.test/mytool", Variant: "minimal",
		Ref: "v0.2.0", TemplateRepo: "r", TemplateCommit: "bbb", VoltVersion: "v0.4.0", At: "t2"}
)

// project scaffolds a project dir from a template root exactly as volt new
// would (render with oldVars), so tests start from the true old state.
func project(t *testing.T, tmplRoot string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "proj")
	if _, err := scaffold.Generate(tmplRoot, "minimal", dir, oldVars); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t *testing.T, proj, oldRoot, newRoot string) Report {
	t.Helper()
	rep, err := Run(proj, oldRoot, newRoot, "minimal", oldVars, newVars, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// ---- merge-scenario matrix ---------------------------------------------

// 1 · Template unchanged → zero actions, project untouched.
func TestUnchangedTemplateIsNoop(t *testing.T) {
	files := map[string]string{"a.txt": "same\n"}
	oldR, newR := templateRoot(t, files), templateRoot(t, files)
	proj := project(t, oldR)
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 0 {
		t.Fatalf("want no actions, got %+v", rep.Results)
	}
}

// 2 · Template changed, user never edited → clean fast-forward.
func TestCleanUpdate(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "v1\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "v2\n"})
	proj := project(t, oldR)
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 1 || rep.Results[0].Outcome != Updated {
		t.Fatalf("%+v", rep.Results)
	}
	if read(t, proj, "a.txt") != "v2\n" {
		t.Fatal("file not fast-forwarded")
	}
}

// 3 · Already at the new content (partial-upgrade re-run) → noop.
func TestAlreadyCurrentIsNoop(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "v1\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "v2\n"})
	proj := project(t, oldR)
	write(t, proj, "a.txt", "v2\n") // user (or a prior run) already updated
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 0 {
		t.Fatalf("%+v", rep.Results)
	}
}

// 4 · User edited one region, template changed another → BOTH survive.
func TestNonOverlappingMerge(t *testing.T) {
	base := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n"
	oldR := templateRoot(t, map[string]string{"a.txt": base})
	newR := templateRoot(t, map[string]string{"a.txt": strings.Replace(base, "line8\n", "line8-template\n", 1)})
	proj := project(t, oldR)
	write(t, proj, "a.txt", strings.Replace(base, "line1\n", "line1-user\n", 1))
	rep := run(t, proj, oldR, newR)
	if rep.Conflicts != 0 || len(rep.Results) != 1 || rep.Results[0].Outcome != Updated {
		t.Fatalf("%+v", rep)
	}
	got := read(t, proj, "a.txt")
	if !strings.Contains(got, "line1-user") || !strings.Contains(got, "line8-template") {
		t.Fatalf("an edit was lost:\n%s", got)
	}
}

// 5 · Both sides changed the SAME region → conflict markers, counted.
func TestOverlappingConflict(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "shared\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "template-version\n"})
	proj := project(t, oldR)
	write(t, proj, "a.txt", "user-version\n")
	rep := run(t, proj, oldR, newR)
	if rep.Conflicts != 1 || rep.Results[0].Outcome != Conflicted {
		t.Fatalf("%+v", rep)
	}
	got := read(t, proj, "a.txt")
	for _, marker := range []string{"<<<<<<< yours (a.txt)", "user-version", "=======", "template-version", ">>>>>>> new template"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("marker %q missing:\n%s", marker, got)
		}
	}
}

// 6 · New template file, absent locally → created.
func TestNewTemplateFileCreated(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "x\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "x\n", "fresh.txt": "new file\n"})
	proj := project(t, oldR)
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 1 || rep.Results[0].Outcome != Updated || rep.Results[0].Path != "fresh.txt" {
		t.Fatalf("%+v", rep.Results)
	}
	if read(t, proj, "fresh.txt") != "new file\n" {
		t.Fatal("content wrong")
	}
}

// 7 · New template file the user ALSO created (differently) → conflict —
// there is no common ancestor to reason from, and silence would eat one side.
func TestNewFileBothCreatedConflicts(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "x\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "x\n", "fresh.txt": "template made this\n"})
	proj := project(t, oldR)
	write(t, proj, "fresh.txt", "user made this\n")
	rep := run(t, proj, oldR, newR)
	if rep.Conflicts != 1 {
		t.Fatalf("%+v", rep)
	}
	got := read(t, proj, "fresh.txt")
	if !strings.Contains(got, "user made this") || !strings.Contains(got, "template made this") {
		t.Fatalf("a creation was lost:\n%s", got)
	}
}

// 8 · Template deleted a file, user never edited it → deleted locally.
func TestTemplateDeletionApplied(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "x\n", "gone.txt": "old\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "x\n"})
	proj := project(t, oldR)
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 1 || rep.Results[0].Outcome != Deleted {
		t.Fatalf("%+v", rep.Results)
	}
	if _, err := os.Stat(filepath.Join(proj, "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
}

// 9 · Template deleted a file the user EDITED → kept; edits are never eaten.
func TestTemplateDeletionKeepsEditedFile(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "x\n", "gone.txt": "old\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "x\n"})
	proj := project(t, oldR)
	write(t, proj, "gone.txt", "user changed this\n")
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 1 || rep.Results[0].Outcome != KeptEdited {
		t.Fatalf("%+v", rep.Results)
	}
	if read(t, proj, "gone.txt") != "user changed this\n" {
		t.Fatal("user's file was eaten")
	}
}

// 10 · USER deleted a file the template changed → stays deleted, reported —
// a deletion is an edit, and edits are never overridden silently.
func TestUserDeletionRespected(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "x\n", "removed.txt": "v1\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "x\n", "removed.txt": "v2\n"})
	proj := project(t, oldR)
	if err := os.Remove(filepath.Join(proj, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 1 || rep.Results[0].Outcome != KeptDeleted {
		t.Fatalf("%+v", rep.Results)
	}
	if _, err := os.Stat(filepath.Join(proj, "removed.txt")); !os.IsNotExist(err) {
		t.Fatal("deletion was overridden")
	}
}

// 11 · Rendered templates diff correctly: the stamp block ([[.Ref]] etc.)
// updates through the normal merge path even when the user edited the file.
func TestStampAdvancesThroughUserEditedFile(t *testing.T) {
	tmpl := map[string]string{".volt.yml.tmpl": "binary: [[.Name]]\nref: [[.Ref]]\n# footer\n"}
	oldR, newR := templateRoot(t, tmpl), templateRoot(t, tmpl)
	proj := project(t, oldR)
	// user appended their own config below the rendered content
	write(t, proj, ".volt.yml", read(t, proj, ".volt.yml")+"internal: false\n")
	rep := run(t, proj, oldR, newR)
	if rep.Conflicts != 0 {
		t.Fatalf("%+v\n%s", rep, read(t, proj, ".volt.yml"))
	}
	got := read(t, proj, ".volt.yml")
	if !strings.Contains(got, "ref: v0.2.0") || !strings.Contains(got, "internal: false") {
		t.Fatalf("stamp or user edit lost:\n%s", got)
	}
}

// 12 · Idempotence: a second run after a clean upgrade does nothing.
func TestSecondRunIsNoop(t *testing.T) {
	oldR := templateRoot(t, map[string]string{"a.txt": "v1\n"})
	newR := templateRoot(t, map[string]string{"a.txt": "v2\n"})
	proj := project(t, oldR)
	run(t, proj, oldR, newR)
	rep := run(t, proj, oldR, newR)
	if len(rep.Results) != 0 {
		t.Fatalf("second run must be a noop: %+v", rep.Results)
	}
}

// ---- stamp parsing ------------------------------------------------------

func fullStamp() map[string]any {
	return map[string]any{
		"by": "volt v0.1.0", "template": "khanakia/volt-cli//templates/minimal",
		"ref": "v0.1.0", "commit": strings.Repeat("a", 40), "at": "2026-08-22T05:00:00Z",
		"inputs": map[string]any{"name": "mytool", "module": "example.test/mytool", "variant": "minimal"},
	}
}

func TestParseStampComplete(t *testing.T) {
	st, err := ParseStamp(fullStamp())
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "mytool" || st.Variant != "minimal" || st.Commit != strings.Repeat("a", 40) {
		t.Fatalf("%+v", st)
	}
	repo, err := st.Repo()
	if err != nil || repo != "khanakia/volt-cli" {
		t.Fatalf("repo = %q, %v", repo, err)
	}
}

func TestParseStampMissingFieldsNamed(t *testing.T) {
	m := fullStamp()
	delete(m, "commit")
	inputs := m["inputs"].(map[string]any)
	delete(inputs, "module")
	_, err := ParseStamp(m)
	if err == nil || !strings.Contains(err.Error(), "commit") || !strings.Contains(err.Error(), "inputs.module") {
		t.Fatalf("missing fields must be NAMED: %v", err)
	}
}

func TestParseStampAbsent(t *testing.T) {
	_, err := ParseStamp(nil)
	if err == nil || !strings.Contains(err.Error(), "volt new") {
		t.Fatalf("absent stamp must point at volt new: %v", err)
	}
}

func TestStampRepoMalformed(t *testing.T) {
	st := Stamp{Template: "not-a-template-path"}
	if _, err := st.Repo(); err == nil {
		t.Fatal("malformed template field must error")
	}
}

// yaml.v3 parses the stamp's RFC3339 `at:` into a time.Time inside the
// opaque map — ParseStamp must convert it back to the exact text volt new
// wrote (hit live on the first real upgrade, 2026-08-22).
func TestParseStampYAMLTimestamp(t *testing.T) {
	m := fullStamp()
	ts, err := time.Parse(time.RFC3339, "2026-08-22T05:22:52Z")
	if err != nil {
		t.Fatal(err)
	}
	m["at"] = ts // what yaml.v3 actually delivers
	st, err := ParseStamp(m)
	if err != nil {
		t.Fatal(err)
	}
	if st.At != "2026-08-22T05:22:52Z" {
		t.Fatalf("at = %q — must reproduce volt new's exact RFC3339 text", st.At)
	}
}
