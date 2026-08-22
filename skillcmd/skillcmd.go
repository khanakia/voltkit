// Package skillcmd provides the `skills` subcommand: list, get, path, check,
// version and refresh over a project's published SKILL.md agent skills.
//
// The content model (docsi/SKILLCMD_SPEC.md): skills are NEVER embedded.
// They live in the repo's skills/ directory (the source) and in the release
// asset skills_<version>.tar.gz (the published form). Every invocation runs
// ensure → clean → serve: fetch this binary-version's bundle into the OS
// cache if absent, delete every other version's cache, serve from real
// files. Staleness against the binary is impossible by construction — the
// check is "does my version's directory exist", never a TTL.
//
// The module knows NOTHING about agent harnesses (Claude, Codex, Cursor…) —
// installation into them belongs to skills.sh. `skills check <dir>` closes
// the freshness loop: the AGENT supplies the path (only it knows where its
// skill file lives), the binary supplies the truth.
package skillcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Options wires skillcmd into a host CLI. Binary, Repo and Version are the
// narrow identity contract — nothing else is imported from any kit.
type Options struct {
	// Binary is the host CLI's name: help text, cache path, env prefix.
	Binary string
	// Repo is the forge-side repository path (may nest on GitLab). It must
	// be compiled in: a binary cannot discover its own origin at runtime.
	Repo string
	// Version is the stamped binary version. A dev shape (see IsDevVersion)
	// switches to live-directory mode: serve the working tree's skills/,
	// no fetch, no cache.
	Version string
	// Tag is the release tag holding the bundle asset. Default: Version
	// (a root-released CLI's bare tag). A CLI released under a prefixed
	// stream passes its own shape, e.g. "lore/"+Version.
	Tag string
	// Env is the override variable naming a live skills directory; when set
	// it bypasses fetch and cache entirely. Default: <BINARY>_SKILLS_DIR.
	Env string
	// AssetURLTemplate is the download URL pattern for one release asset,
	// with literal {tag} and {asset} placeholders — e.g. GitHub's
	// "https://github.com/o/n/releases/download/{tag}/{asset}" or GitLab's
	// "https://gitlab.com/o/n/-/releases/{tag}/downloads/{asset}". Written
	// by `volt gen skills` from the repo's detected forge. Empty → the
	// GitHub shape derived from Repo (compatibility with wirings generated
	// before forges existed).
	AssetURLTemplate string
	// Fetcher retrieves published bundles. Default: HTTPS via
	// AssetURLTemplate.
	// Tests substitute a fake; nothing in this package requires a network.
	Fetcher Fetcher
	// CacheRoot overrides os.UserCacheDir (tests). Empty → the real one.
	CacheRoot string
	// WorkDir is where live-directory discovery starts (dev mode).
	// Empty → the process working directory.
	WorkDir string
}

// applyDefaults fills derivable fields; the required trio is validated by
// New, loudly, because a half-wired skills command fails only at use time.
func (o *Options) applyDefaults() {
	if o.Env == "" {
		o.Env = strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(o.Binary)) + "_SKILLS_DIR"
	}
	if o.Tag == "" {
		o.Tag = o.Version
	}
	if o.AssetURLTemplate == "" {
		o.AssetURLTemplate = "https://github.com/" + o.Repo + "/releases/download/{tag}/{asset}"
	}
	if o.Fetcher == nil {
		o.Fetcher = urlFetcher{tpl: o.AssetURLTemplate}
	}
}

// New builds the `skills` command tree for the host CLI.
func New(o Options) *cobra.Command {
	o.applyDefaults()
	root := &cobra.Command{
		Use:   "skills",
		Short: "List and retrieve this tool's bundled agent skills",
		Long: fmt.Sprintf(`Serves %s's agent skills (SKILL.md format), always matched to this
binary's version (%s): content is fetched once per version from the
project's release and cached; a binary upgrade re-syncs automatically.

Install into an agent harness with skills.sh:  npx skills add %s
Verify an installed copy:                      %s skills check <dir>`,
			o.Binary, o.Version, o.Repo, o.Binary),
		// Bare `skills` behaves as `skills list` — the agent-browser
		// convention this interface follows.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, o, false)
		},
	}
	root.AddCommand(newListCommand(o))
	root.AddCommand(newGetCommand(o))
	root.AddCommand(newPathCommand(o))
	root.AddCommand(newCheckCommand(o))
	root.AddCommand(newVersionCommand(o))
	root.AddCommand(newRefreshCommand(o))
	// An operational failure (STALE, offline, unknown skill) must not drown
	// its message in a usage dump — usage is for argument mistakes, which
	// cobra still reports through the returned error. Set on every command:
	// cobra consults the FAILING command's flag, not the root's.
	root.SilenceUsage = true
	for _, c := range root.Commands() {
		c.SilenceUsage = true
	}
	return root
}

// Serving sources — the closed set `resolveDir` returns and `skills
// version` reports. Constants so output, tests and future branching share
// one spelling (SourceEnv is a PREFIX: the env var name follows it).
const (
	SourceEnvPrefix = "env:"
	SourceLive      = "live"
	SourceCache     = "cache"
)

// IsDevVersion reports whether v is a working-tree build rather than a
// release: "dev", empty, or any v0.0.0-dev.* stamp. Dev builds serve the
// live skills/ directory — the working tree IS their truth.
func IsDevVersion(v string) bool {
	return v == "" || v == "dev" || strings.Contains(v, "-dev")
}

// resolveDir returns the directory skills are served from this invocation,
// running ensure→clean when the cache is the source. The three sources, in
// precedence order, each deliberate:
//
//  1. env override    — debugging and development trump everything
//  2. dev version     — the working tree's skills/ is a dev build's truth
//  3. version cache   — fetch-once-per-version, the normal path
func resolveDir(o Options) (dir string, source string, err error) {
	if v := os.Getenv(o.Env); v != "" {
		// Validate NOW: an override pointing nowhere must fail here with
		// the variable named — not surface later as a confusing read error,
		// and never let `path` print a directory that does not exist.
		if st, err := os.Stat(v); err != nil || !st.IsDir() {
			return "", "", fmt.Errorf("%s=%s is not a directory", o.Env, v)
		}
		return v, SourceEnvPrefix + o.Env, nil
	}
	if IsDevVersion(o.Version) {
		d, err := findLiveDir(o.WorkDir)
		if err != nil {
			return "", "", err
		}
		return d, SourceLive, nil
	}
	d, err := ensure(o)
	if err != nil {
		return "", "", err
	}
	return d, SourceCache, nil
}

// findLiveDir walks up from start looking for a skills/ directory — the dev
// build's source. Hard error when absent: a dev build outside its repo has
// no truth to serve, and guessing would violate skeleton honesty.
func findLiveDir(start string) (string, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "skills")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("dev build: no skills/ directory found from %s upward — run inside the project, or set the skills-dir env override", start)
		}
	}
}
