// model.go — the skill data model: discovery, frontmatter, file listing.
package skillcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is one loaded skill.
type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Dir         string   `json:"-"`     // absolute directory (or "" for single-file sugar)
	SkillMD     string   `json:"-"`     // absolute path to the SKILL.md (or the bare .md)
	Files       []string `json:"files"` // supporting files, relative, sorted — SKILL.md excluded
}

// skillFileName is the required per-skill document of the open standard.
const skillFileName = "SKILL.md"

// LoadAll discovers every skill under root: each subdirectory holding a
// SKILL.md, plus bare *.md files as single-file sugar. Hidden entries
// (dotfiles) are ignored everywhere — installed and source trees accumulate
// .DS_Store-class junk that must never affect behaviour.
func LoadAll(root string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("skills directory %s: %w", root, err)
	}
	var out []Skill
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		switch {
		case e.IsDir():
			dir := filepath.Join(root, e.Name())
			// A dir WITHOUT a SKILL.md is skipped deliberately — it may be
			// scaffolding in progress; the ci lint is where malformed
			// content FAILS, serving stays permissive. But ONLY the
			// not-exists case skips: any other stat error (permissions,
			// I/O) propagates — conflating them silently narrowed the
			// listing (caught by the edge-case suite, 2026-08-22).
			if _, err := os.Stat(filepath.Join(dir, skillFileName)); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("skill %s: %w", e.Name(), err)
			}
			sk, err := loadDirSkill(dir)
			if err != nil {
				return nil, err
			}
			out = append(out, *sk)
		case strings.HasSuffix(e.Name(), ".md"):
			sk, err := loadFileSkill(filepath.Join(root, e.Name()))
			if err != nil {
				return nil, err
			}
			out = append(out, *sk)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	// Duplicate names would make `get <name>` ambiguous — refuse loudly
	// rather than serving whichever sorted first.
	for i := 1; i < len(out); i++ {
		if out[i].Name == out[i-1].Name {
			return nil, fmt.Errorf("two skills named %q — names must be unique", out[i].Name)
		}
	}
	return out, nil
}

// loadDirSkill reads one directory skill. Total by contract: the caller has
// already verified SKILL.md exists, so this returns a skill or a real error
// — never the nil,nil ambiguity.
func loadDirSkill(dir string) (*Skill, error) {
	mdPath := filepath.Join(dir, skillFileName)
	sk, err := skillFromMD(mdPath, filepath.Base(dir))
	if err != nil {
		return nil, err
	}
	sk.Dir = dir
	// Supporting files: everything under the dir except SKILL.md and
	// hidden entries, relative paths, sorted (deterministic output).
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || path == mdPath {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		sk.Files = append(sk.Files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(sk.Files)
	return sk, nil
}

// loadFileSkill treats a bare skills/<name>.md as a skill whose whole
// content is its SKILL.md — the single-file sugar of the spec.
func loadFileSkill(path string) (*Skill, error) {
	base := strings.TrimSuffix(filepath.Base(path), ".md")
	return skillFromMD(path, base)
}

// skillFromMD parses the frontmatter identity. Fallbacks are deliberate:
// serving stays permissive (name ← the dir/file name, description ← "");
// the ci lint is the layer that REQUIRES proper frontmatter before content
// ships. Two layers, two jobs.
func skillFromMD(mdPath, fallbackName string) (*Skill, error) {
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, err
	}
	fm := parseFrontmatter(string(raw))
	sk := &Skill{Name: fallbackName, SkillMD: mdPath}
	if v := fm["name"]; v != "" {
		sk.Name = v
	}
	sk.Description = fm["description"]
	return sk, nil
}

// parseFrontmatter extracts top-level "key: value" pairs from a leading
// "---" block. Hand-rolled on purpose: the two keys the standard requires
// are flat scalars, and a YAML dependency for that would be the package's
// only heavy import. Nested structures are ignored, not errors.
func parseFrontmatter(content string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return out
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // nested YAML — not ours to interpret
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return out
}

// find returns the named skill or an error listing what exists — an unknown
// name must never be a silent empty result.
func find(skills []Skill, name string) (Skill, error) {
	var names []string
	for _, s := range skills {
		if s.Name == name {
			return s, nil
		}
		names = append(names, s.Name)
	}
	return Skill{}, fmt.Errorf("no skill named %q (have: %s)", name, strings.Join(names, ", "))
}
