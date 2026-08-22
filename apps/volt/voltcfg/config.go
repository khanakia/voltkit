// Package voltcfg loads the optional per-directory .volt.yml.
//
// The governing rule (docsi/RELEASE_PIPELINE_SPEC.md, "Configuration"): if
// volt can detect a value, it is not a field. Everything here is therefore a
// default-with-override — a repo with no .volt.yml gets a complete, working
// configuration, and the file only ever narrows or renames.
package voltcfg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the per-directory config file, looked up beside the released
// directory (not at the repo root: in a monorepo each CLI configures itself).
const FileName = ".volt.yml"

// Config is everything .volt.yml can express for build/release. Zero value +
// ApplyDefaults is the no-config path.
type Config struct {
	// Platforms lists GOOS/GOARCH targets as "os/arch" strings.
	// Empty → platform.Default at the call site.
	Platforms []string `yaml:"platforms"`
	// Binary overrides the output name. Empty → the released directory's
	// base name. Also the documented fix for two same-named binaries in one
	// repo (spec, "Resolving a tag back to a directory").
	Binary string `yaml:"binary"`
	// ExtraFiles are shipped inside each archive next to the binary
	// (README, LICENSE). Paths are relative to the released directory.
	ExtraFiles []string `yaml:"extra_files"`

	LDFlags LDFlags `yaml:"ldflags"`

	// CGO enables cgo builds — see the spec's cgo section for what that
	// costs. Default false: pure Go is what lets one machine build every
	// platform.
	CGO       bool      `yaml:"cgo"`
	Toolchain Toolchain `yaml:"toolchain"`

	// Brew configures the Homebrew channel; zero value disables it.
	// Field shapes mirror publish.BrewConfig (converted at the call site so
	// this package keeps zero imports beyond yaml).
	Brew BrewConfig `yaml:"brew"`

	// Hooks are project-owned scripts volt release executes at fixed points.
	// This is the escape hatch that keeps volt's surface flat: anything
	// custom — promoting a release to a public repo, notifications, mirrors
	// — is a script the project writes, never a volt feature (decided
	// 2026-08-22, superseding the split-repo publishing design).
	Hooks HooksConfig `yaml:"hooks"`

	// Skills configures the skills directory (SKILLCMD_SPEC). Zero value:
	// managed-if-present, directory "skills".
	Skills SkillsConfig `yaml:"skills"`

	// Internal marks a directory (and everything under it) as never
	// released: hidden from `volt status`, refused by `volt release`, and
	// invisible to tag resolution. Intent, not fact — the one thing the
	// "if volt can detect it, it is not a field" rule cannot detect.
	Internal bool `yaml:"internal"`

	// Forge names the code-host implementation ("github", "gitlab") when the
	// origin host cannot be auto-detected — self-hosted instances only.
	// gitlab.com and github.com detect with zero config; a typo here is a
	// hard error (forge.ByName enumerates valid names). Meaningful only in
	// the REPO ROOT's .volt.yml: the forge is a repo-level fact.
	Forge string `yaml:"forge"`

	// Generated is the scaffold stamp `volt new` writes (ADR-R13): which
	// template, ref, commit and inputs produced this project — the data a
	// future `volt upgrade` needs. Opaque here: volt reads it back only in
	// upgrade tooling, and build/release must not depend on it.
	Generated map[string]any `yaml:"generated"`
}

// HooksConfig is the `hooks:` block. Paths are repo-root-relative and run
// with the repo root as working directory, so one script can serve many
// streams. Scripts are executed directly (any executable works — sh,
// python, a binary); volt streams their output and passes context via
// VOLT_* environment variables.
type HooksConfig struct {
	// PreRelease runs AFTER volt's own gate (tests) and BEFORE anything
	// permanent (the tag reserve). Its failure aborts the release with
	// nothing created — it is the project's own last-chance gate.
	PreRelease string `yaml:"pre_release"`
	// PostRelease runs after the release has published and verified. Its
	// failure cannot un-release anything: volt exits non-zero saying so,
	// and re-running the script (or --from-tag) is the recovery.
	PostRelease string `yaml:"post_release"`
}

// SkillsConfig is the `skills:` block.
type SkillsConfig struct {
	// Managed false = skills/ exists but volt must leave it alone: the ci
	// lint skips (loudly) and `volt gen skills` refuses. Pointer so the
	// absent-field default (managed) is distinct from explicit false.
	Managed *bool `yaml:"managed"`
	// Dir overrides the directory name (default "skills").
	Dir string `yaml:"dir"`
}

// ManagedDisabled reports an explicit `managed: false`.
func (s SkillsConfig) ManagedDisabled() bool { return s.Managed != nil && !*s.Managed }

// SkillsDir returns the configured directory name.
func (s SkillsConfig) SkillsDir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return "skills"
}

// BrewConfig is the `brew:` channel block.
type BrewConfig struct {
	Tap         string `yaml:"tap"`
	Description string `yaml:"description"`
	License     string `yaml:"license"`
}

// LDFlags controls linker flags. Every -X target is data here, never a
// constant in code (ADR-R08): the default map stamps ubgo/buildinfo because
// that is what the kit uses, and any project replaces the map wholesale.
type LDFlags struct {
	// Strip controls -s -w. Pointer so YAML can distinguish "unset (default
	// true)" from an explicit `strip: false` for debug builds.
	Strip *bool `yaml:"strip"`
	// Vars maps ldflags -X symbol → template ("{{.Version}}").
	// A non-empty map REPLACES the default entirely — merging would make it
	// impossible to opt out of the buildinfo symbols.
	Vars map[string]string `yaml:"vars"`
	// Extra is raw passthrough appended after everything else — the escape
	// hatch for flags this schema does not model.
	Extra []string `yaml:"extra"`
}

// Toolchain names the C compiler templates used when CGO is true.
// {{.ZigTarget}} is the intended variable; a per-platform render happens in
// the builder.
type Toolchain struct {
	CC  string `yaml:"cc"`
	CXX string `yaml:"cxx"`
}

// DefaultLDFlagsVars is the out-of-the-box stamp map. It targets the
// ubgo/buildinfo package path — never `main` — so the flags stay identical no
// matter what a scaffolded project renames its binary to. Projects that stamp
// their own symbols replace this map via `ldflags.vars`.
func DefaultLDFlagsVars() map[string]string {
	return map[string]string{
		"github.com/ubgo/buildinfo.Version":   "{{.Version}}",
		"github.com/ubgo/buildinfo.Commit":    "{{.Commit}}",
		"github.com/ubgo/buildinfo.BuildTime": "{{.BuildTime}}",
	}
}

// Load reads dir/.volt.yml. A missing file is the normal case and returns a
// defaulted Config; a present-but-malformed file is a hard error — falling
// back to defaults on a parse error would silently ignore the user's intent,
// the exact failure mode the spec bans ("a malformed manifest must fail
// loudly").
func Load(dir string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
		// no file — pure defaults
	case err != nil:
		return c, fmt.Errorf("read %s: %w", FileName, err)
	default:
		// KnownFields: a typo like `platfroms:` must error, not be ignored.
		dec := yaml.NewDecoder(bytesReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil {
			// A comment-only or empty file decodes as io.EOF — that is a
			// legitimate "all defaults" config, not a parse failure.
			if !errors.Is(err, io.EOF) {
				return c, fmt.Errorf("parse %s in %s: %w", FileName, dir, err)
			}
		}
	}
	c.ApplyDefaults(dir)
	return c, nil
}

// ApplyDefaults fills every unset field so callers never branch on "was this
// configured". dir supplies the binary-name default.
func (c *Config) ApplyDefaults(dir string) {
	if c.Binary == "" {
		// The released directory's base name; "." resolves via Abs so a
		// root-level release still gets a real name.
		abs, err := filepath.Abs(dir)
		if err == nil {
			c.Binary = filepath.Base(abs)
		}
	}
	if c.LDFlags.Strip == nil {
		t := true
		c.LDFlags.Strip = &t
	}
	if len(c.LDFlags.Vars) == 0 {
		c.LDFlags.Vars = DefaultLDFlagsVars()
	}
}

// IsInternal reports whether dir, or any ancestor up to root, carries
// `internal: true`. Inheritance by prefix is the point: marking `dbent`
// internal also hides `dbent/cmd/entg` without a second file.
func IsInternal(root, dir string) bool {
	abs := filepath.Join(root, dir)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	for d, _ := filepath.Abs(abs); ; d = filepath.Dir(d) {
		if internalHere(d) {
			return true
		}
		if d == rootAbs || filepath.Dir(d) == d {
			return false
		}
	}
}

// internalHere reads just the `internal:` field — a full Load would reject
// files during mid-walk edits and costs more than this needs.
func internalHere(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "internal:"); ok {
			return strings.TrimSpace(v) == "true"
		}
	}
	return false
}
