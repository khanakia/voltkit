// Package forge is the ONE seam between volt and any code host.
//
// Why it exists: volt grew up on GitHub, and forge-specific knowledge (URL
// shapes, auth probing, the publish driver) was spreading through command
// code. This package fences it: everything forge-specific lives behind the
// Forge interface, GitHub is merely the first implementation, and adding
// GitLab/Gitea later is "write one struct", not "untangle the orchestrator".
// Full design + provider scoping: docs/proposals/forge-provider.md.
//
// The rule this package enforces (treat as an ADR): NOTHING outside forge/
// may name a forge — no github.com literals, no `gh` invocations, no
// forge-conditional branches in command or orchestration code. publish/ is
// grandfathered as the GitHub publish driver that forge.GitHub returns; new
// forge-touching code goes here.
package forge

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/genfiles"
)

// Repo is an "owner/name" repository identity. A distinct type rather than a
// bare string so a repo can't be confused with the many other strings
// (tags, dirs, URLs) flowing through release code.
type Repo string

// String returns the "owner/name" form used in URLs and generated files.
func (r Repo) String() string { return string(r) }

// Check is one doctor probe's outcome. Doctor() runs the probes and returns
// results rather than exposing probe internals, so `volt doctor` can print
// any forge's checks without knowing what they are.
type Check struct {
	OK   bool
	Good string // printed on ok, names what is healthy
	Bad  string // printed on failure, MUST name the fix (doctor's contract)
}

// Forge is everything volt needs from a code host. The method set is
// deliberately exactly what volt routes today — no speculative surface; it
// grows when a second forge lands (see the proposal for the target shape,
// including capability flags for hosts where a concept is missing).
type Forge interface {
	// Name identifies the implementation ("github") — used by the
	// `forge:` config override and in doctor output.
	Name() string

	// ParseRemote extracts the repo from a git remote URL, reporting
	// whether the URL belongs to this forge. Non-matching URLs return
	// ("", false) — guessing would generate wrong URLs into files.
	ParseRemote(url string) (Repo, bool)

	// RepoOf resolves the repo identity for the working copy at dir,
	// using the forge's most authoritative route first (an API view
	// resolves renames) before falling back to parsing the origin URL.
	// "" when the directory has no resolvable remote on this forge.
	RepoOf(dir string) Repo

	// Publisher returns the release-publishing driver for dir. The
	// publish.Publisher interface stays in package publish so the release
	// orchestrator and its fakes never import forge.
	Publisher(dir string) Publisher

	// AssetURL is the direct download URL for one released asset — the
	// shape install scripts, self-update and skills fetching depend on.
	AssetURL(repo Repo, tag, asset string) string

	// FileURL links a file in the repo's default branch — generated
	// release notes use it for the "See CHANGELOG.md" line.
	FileURL(repo Repo, path string) string

	// LatestAssetURL is the unauthenticated "latest release" redirect for
	// an asset, or "" when the forge has no such concept (that absence is
	// real: see the proposal's provider table) — callers must branch on
	// empty rather than assume.
	LatestAssetURL(repo Repo, asset string) string

	// RawFileURL is the curl-able raw-content URL of a file at the default
	// branch — install scripts print it as their own "curl | sh" hint.
	RawFileURL(repo Repo, path string) string

	// CIFiles is this forge's generated CI file set (FG-D2) — rendered by
	// `volt gen` through the shared hash-guard engine in genfiles.
	CIFiles() []genfiles.File

	// Doctor runs this forge's release-readiness probes (CLI present,
	// authenticated, …). Read-only.
	Doctor() []Check
}

// Publisher mirrors publish.Publisher so this package does not import
// publish (which would invert the dependency: publish is a driver forge
// hands out, not a dependency of the seam). Kept structurally identical —
// forge.GitHub returns a value that satisfies both.
type Publisher interface {
	ReleaseExists(tag string) bool
	CreateOrUpdate(tag, title, notesFile string, assets []string) error
	FetchBody(tag string) (string, error)
	FetchAssetNames(tag string) ([]string, error)
}

// registry holds every known forge, in detection order. Package-level and
// fixed at compile time: forges are code, not plugins (a custom forge is a
// new implementation here, or a volt-<name> plugin per ADR-R12).
var registry = []Forge{GitHub{}, GitLab{}}

// ByName resolves the `forge:` config override. The error enumerates valid
// names — a typo must never fall back silently (it would publish to the
// wrong forge).
func ByName(name string) (Forge, error) {
	var known []string
	for _, f := range registry {
		if f.Name() == name {
			return f, nil
		}
		known = append(known, f.Name())
	}
	return nil, fmt.Errorf("unknown forge %q — known: %s", name, strings.Join(known, ", "))
}

// Detect picks the forge for the working copy at dir: an explicit override
// (the repo root's `forge:` config, when the caller loaded one) wins;
// otherwise the origin remote URL is matched against each known forge.
//
// Rules, per FG-D6 (the second forge made them enforceable):
//   - no remote at all → Default(): a local-only repo can still build, and
//     there is nothing to contradict the assumption yet;
//   - remote on a RECOGNIZED host → that forge, zero config;
//   - remote on an UNRECOGNIZED host → hard error naming the `forge:` fix.
//     Guessing here is how releases land on the wrong forge — the
//     pre-seam behavior this flip deliberately retires.
func Detect(dir, override string) (Forge, error) {
	if override != "" {
		return ByName(override)
	}
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return Default(), nil
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return Default(), nil
	}
	for _, f := range registry {
		if _, ok := f.ParseRemote(url); ok {
			return f, nil
		}
	}
	var known []string
	for _, f := range registry {
		known = append(known, f.Name())
	}
	return nil, fmt.Errorf("origin remote %q matches no known forge (%s) — set `forge:` in .volt.yml at the repo root (self-hosted hosts cannot be auto-detected)", url, strings.Join(known, ", "))
}

// Default returns the forge volt assumes when detection has nothing to work
// with. A named function rather than a scattered GitHub{} literal, so the
// assumption has one home to revisit.
func Default() Forge { return GitHub{} }
