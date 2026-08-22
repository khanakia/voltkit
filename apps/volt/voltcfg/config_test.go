package voltcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Binary != "notes" {
		t.Errorf("Binary default = %q, want directory name", c.Binary)
	}
	if c.LDFlags.Strip == nil || !*c.LDFlags.Strip {
		t.Error("Strip must default to true")
	}
	if _, ok := c.LDFlags.Vars["github.com/ubgo/buildinfo.Version"]; !ok {
		t.Error("default vars must stamp ubgo/buildinfo")
	}
	if c.CGO {
		t.Error("CGO must default to false")
	}
}

func TestLoadOverridesReplaceVarsWholesale(t *testing.T) {
	dir := t.TempDir()
	yml := `
binary: mycli
platforms: [linux/amd64]
ldflags:
  strip: false
  vars:
    main.version: "{{.Version}}"
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Binary != "mycli" || len(c.Platforms) != 1 {
		t.Fatalf("overrides not applied: %+v", c)
	}
	if *c.LDFlags.Strip {
		t.Error("explicit strip: false must survive defaulting")
	}
	// The user's map REPLACES the default — buildinfo symbols must be gone.
	if len(c.LDFlags.Vars) != 1 {
		t.Fatalf("vars must replace, not merge: %v", c.LDFlags.Vars)
	}
}

// A malformed or typo'd config must fail loudly, never fall back to defaults.
func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("platfroms: [linux/amd64]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("want error on unknown field 'platfroms', got nil")
	}
}

// A comment-only .volt.yml is a legitimate all-defaults config — hit for
// real: apps/demo carries one purely to document why it has no overrides.
func TestLoadCommentOnlyFileIsDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("# only a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Binary != "notes" {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

// The scaffold stamp `volt new` writes must parse — KnownFields(true) would
// otherwise reject every scaffolded project's config (hit live on the first
// volt new snapshot, 2026-08-22).
func TestLoadAcceptsGeneratedStamp(t *testing.T) {
	dir := t.TempDir()
	yml := "binary: mytool\ngenerated:\n  by: volt v0.4.0\n  inputs:\n    name: mytool\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Binary != "mytool" || c.Generated["by"] != "volt v0.4.0" {
		t.Fatalf("stamp not parsed: %+v", c)
	}
}

// SkillsConfig's tri-state: absent (managed), explicit false, explicit true —
// and the directory default.
func TestSkillsConfigDefaultsAndOptOut(t *testing.T) {
	var zero SkillsConfig
	if zero.ManagedDisabled() {
		t.Fatal("absent managed must mean managed")
	}
	if zero.SkillsDir() != "skills" {
		t.Fatalf("dir default: %q", zero.SkillsDir())
	}
	f := false
	if !(SkillsConfig{Managed: &f}).ManagedDisabled() {
		t.Fatal("explicit false must disable")
	}
	tr := true
	if (SkillsConfig{Managed: &tr}).ManagedDisabled() {
		t.Fatal("explicit true must stay managed")
	}
	if (SkillsConfig{Dir: "agent-skills"}).SkillsDir() != "agent-skills" {
		t.Fatal("dir override lost")
	}
}
