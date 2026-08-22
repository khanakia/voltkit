// skills.go — `volt gen skills`: the wiring file for voltkit/skillcmd plus a
// one-time starter skill (SKILLCMD_SPEC, "volt gen skills").
package genfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillsWiring is the hash-guarded wiring file target. Separate from
// Registry: it is generated only by the explicit `volt gen skills`, never by
// a bare `volt gen` (a repo without skills must not sprout the subcommand).
var SkillsWiring = File{RelPath: "skills_gen.go", Template: "skills_gen.go.tpl", Comment: "//"}

// SkillsVars parameterise the skills templates.
type SkillsVars struct {
	Binary string
	Repo   string
	Tag    string // the release-tag shape holding bundles, e.g. "volt/"+version or bare
	// AssetTpl is the forge's asset-download URL pattern with {tag}/{asset}
	// placeholders — compiled into the wiring so the built binary fetches
	// from the RIGHT forge without ever detecting one at runtime.
	AssetTpl string
	Version  string // volt's own version, for the header
}

// GenerateSkillsWiring renders the guarded wiring file.
func GenerateSkillsWiring(rootDir string, sv SkillsVars, force bool) (Result, error) {
	v := Vars{Repo: sv.Repo, Binary: sv.Binary, Version: sv.Version}
	return generateWith(rootDir, SkillsWiring, v, map[string]string{"Tag": sv.Tag, "AssetTpl": sv.AssetTpl}, force)
}

// StarterSkill writes skills/<binary>-core/SKILL.md when the skills
// directory is absent — created ONCE, then never touched again: it is the
// project's content, and only the wiring file carries the hash guard.
// Returns the created path, or "" when skills/ already existed.
func StarterSkill(rootDir string, sv SkillsVars) (string, error) {
	skillsDir := filepath.Join(rootDir, "skills")
	if _, err := os.Stat(skillsDir); err == nil {
		return "", nil // exists — the project owns it, hands off
	}
	raw, err := templates.ReadFile("templates/starter_skill.md.tpl")
	if err != nil {
		return "", err
	}
	rendered, err := renderBytes(string(raw), Vars{Repo: sv.Repo, Binary: sv.Binary, Version: sv.Version}, nil)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(skillsDir, sv.Binary+"-core", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, rendered, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// LintSkills validates a skills directory for shipping: every skill must
// carry name + description frontmatter (a skill without them is invisible
// to discovery), names must be unique, and the binary-prefix convention
// WARNS only. Returns problems (fail) and warnings (report).
//
// Deliberately re-implemented rather than importing voltkit/skillcmd:
// ADR-R08 — volt's commands import nothing from the kit, so volt keeps
// working on any repo without version-coupling to kit modules. The ~30
// duplicated lines are the price of that boundary, paid knowingly.
func LintSkills(skillsDir, binary string) (problems, warnings []string, err error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]string{}
	for _, e := range entries {
		if e.Name()[0] == '.' {
			continue
		}
		var mdPath, fallback string
		switch {
		case e.IsDir():
			mdPath = filepath.Join(skillsDir, e.Name(), "SKILL.md")
			fallback = e.Name()
			if _, err := os.Stat(mdPath); err != nil {
				problems = append(problems, fmt.Sprintf("skills/%s/ has no SKILL.md — not a skill, not scaffolding volt recognises", e.Name()))
				continue
			}
		case filepath.Ext(e.Name()) == ".md":
			mdPath = filepath.Join(skillsDir, e.Name())
			fallback = e.Name()[:len(e.Name())-3]
		default:
			continue
		}
		fm, err := readFrontmatter(mdPath)
		if err != nil {
			return nil, nil, err
		}
		name := fm["name"]
		if name == "" {
			problems = append(problems, fmt.Sprintf("%s: missing `name:` frontmatter — the skill is invisible to discovery", mdPath))
			name = fallback
		}
		if fm["description"] == "" {
			problems = append(problems, fmt.Sprintf("%s: missing `description:` frontmatter — agents cannot decide when to use it", mdPath))
		}
		if prev, dup := seen[name]; dup {
			problems = append(problems, fmt.Sprintf("duplicate skill name %q (%s and %s)", name, prev, mdPath))
		}
		seen[name] = mdPath
		if binary != "" && !strings.HasPrefix(name, binary+"-") {
			warnings = append(warnings, fmt.Sprintf("%s: name %q lacks the %q prefix — collides easily in shared skill directories", mdPath, name, binary+"-"))
		}
	}
	return problems, warnings, nil
}

// readFrontmatter extracts flat "key: value" pairs from a leading --- block.
func readFrontmatter(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return out, nil
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // nested YAML — not ours to interpret
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out, nil
}
