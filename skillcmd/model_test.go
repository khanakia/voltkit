package skillcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates rel under root with parents.
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

// skillsFixture builds a realistic skills tree: two dir skills (one with
// references), a single-file sugar skill, and junk that must be invisible.
func skillsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "lore-core/SKILL.md", "---\nname: lore-core\ndescription: Core usage. Read this first.\n---\n\n# core body\n")
	write(t, root, "lore-core/references/commands.md", "all the commands\n")
	write(t, root, "lore-search/SKILL.md", "---\nname: lore-search\ndescription: Search things\n---\n\nsearch body\n")
	write(t, root, "quickref.md", "---\nname: quickref\ndescription: One-pager\n---\n\nquick body\n")
	// Junk that must never surface anywhere:
	write(t, root, ".DS_Store", "junk")
	write(t, root, "lore-core/.DS_Store", "junk")
	write(t, root, ".hidden-dir/SKILL.md", "---\nname: ghost\n---\n")
	// A scaffolding dir without SKILL.md — skipped silently, not an error.
	write(t, root, "wip-dir/notes.txt", "not a skill yet")
	return root
}

func TestLoadAllDiscovery(t *testing.T) {
	skills, err := LoadAll(skillsFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}
	// Sorted; sugar file included; hidden + SKILL.md-less dirs absent.
	if strings.Join(names, ",") != "lore-core,lore-search,quickref" {
		t.Fatalf("names: %v", names)
	}
}

func TestLoadAllFilesExcludeJunkAndSkillMD(t *testing.T) {
	skills, _ := LoadAll(skillsFixture(t))
	core, err := find(skills, "lore-core")
	if err != nil {
		t.Fatal(err)
	}
	if len(core.Files) != 1 || core.Files[0] != "references/commands.md" {
		t.Fatalf("files must list supporting files only (no SKILL.md, no dotfiles): %v", core.Files)
	}
}

func TestFrontmatterFallbacks(t *testing.T) {
	root := t.TempDir()
	// No name in frontmatter → dir name; no frontmatter at all → same.
	write(t, root, "unnamed/SKILL.md", "---\ndescription: has description only\n---\nbody\n")
	write(t, root, "bare/SKILL.md", "just a body, no frontmatter\n")
	skills, err := LoadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	if byName["unnamed"].Description != "has description only" {
		t.Fatalf("description lost: %+v", byName["unnamed"])
	}
	if _, ok := byName["bare"]; !ok {
		t.Fatal("frontmatterless skill must fall back to dir name (lint, not serving, enforces format)")
	}
}

func TestDuplicateNamesRefused(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/SKILL.md", "---\nname: same\n---\n")
	write(t, root, "b/SKILL.md", "---\nname: same\n---\n")
	if _, err := LoadAll(root); err == nil || !strings.Contains(err.Error(), "same") {
		t.Fatalf("duplicate names must refuse, naming the name: %v", err)
	}
}

func TestFindUnknownNamesExisting(t *testing.T) {
	skills, _ := LoadAll(skillsFixture(t))
	_, err := find(skills, "ghost")
	if err == nil || !strings.Contains(err.Error(), "lore-core") {
		t.Fatalf("unknown-name error must list what exists: %v", err)
	}
}

func TestParseFrontmatterEdgeCases(t *testing.T) {
	// Quoted values unquoted; nested yaml ignored; body colons ignored.
	fm := parseFrontmatter("---\nname: \"quoted\"\nmeta:\n  nested: skipped\ndescription: a: b\n---\nbody: not me\n")
	if fm["name"] != "quoted" {
		t.Errorf("quotes must strip: %q", fm["name"])
	}
	if _, ok := fm["nested"]; ok {
		t.Error("nested yaml must be ignored")
	}
	if fm["description"] != "a: b" {
		t.Errorf("only the FIRST colon splits: %q", fm["description"])
	}
	if len(parseFrontmatter("no frontmatter here")) != 0 {
		t.Error("no leading --- means no frontmatter")
	}
}
