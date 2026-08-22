// github.go — the GitHub implementation of Forge: the URL shapes, remote
// parsing, auth probes and publish driver that previously lived inline in
// command code. Behavior-identical to the pre-seam code by construction.
package forge

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/genfiles"
	"github.com/khanakia/voltkit/apps/volt/publish"
)

// GitHub is the github.com implementation. Zero-value usable: the host is
// fixed because GitHub Enterprise (custom hosts) is exactly the kind of
// variant that arrives with a real consumer, not speculatively.
type GitHub struct{}

// Name implements Forge.
func (GitHub) Name() string { return "github" }

// ParseRemote implements Forge for the two URL shapes git uses:
// https://github.com/owner/name(.git) and git@github.com:owner/name(.git).
func (GitHub) ParseRemote(url string) (Repo, bool) {
	var rest string
	switch {
	case strings.HasPrefix(url, "https://github.com/"):
		rest = strings.TrimPrefix(url, "https://github.com/")
	case strings.HasPrefix(url, "git@github.com:"):
		rest = strings.TrimPrefix(url, "git@github.com:")
	default:
		return "", false
	}
	rest = strings.TrimSuffix(rest, ".git")
	if strings.Count(rest, "/") != 1 {
		return "", false
	}
	return Repo(rest), true
}

// RepoOf implements Forge. Two rungs: gh (authoritative — resolves renames),
// then parsing the origin URL directly — so generation works on a repo whose
// remote is configured but not yet pushed to GitHub.
func (g GitHub) RepoOf(dir string) Repo {
	cmd := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return Repo(s)
		}
	}
	raw, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	repo, _ := g.ParseRemote(strings.TrimSpace(string(raw)))
	return repo
}

// Publisher implements Forge — the gh-CLI release driver. Returned as the
// neutral forge.Publisher; publish.GH also satisfies publish.Publisher, so
// release code keeps its own interface and fakes untouched.
func (GitHub) Publisher(dir string) Publisher { return publish.GH{Dir: dir} }

// AssetURL implements Forge: the browser-download shape, which is an
// unauthenticated redirect with NO API rate limit — the reason install
// scripts and self-update use it instead of the JSON API (60 req/hr
// anonymous, shared per source IP: CI fleets hit it).
func (GitHub) AssetURL(repo Repo, tag, asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, asset)
}

// LatestAssetURL implements Forge. Note "latest" is repo-global: in a
// monorepo it may point at ANY stream's newest release, which is why
// self-update resolves its exact tag instead of using this.
func (GitHub) LatestAssetURL(repo Repo, asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", repo, asset)
}

// Doctor implements Forge: publishing needs gh present AND authenticated —
// installed-but-logged-out fails at the worst moment (mid-release), which is
// exactly what doctor exists to pre-empt.
func (GitHub) Doctor() []Check {
	var checks []Check
	_, ghErr := exec.LookPath("gh")
	checks = append(checks, Check{
		OK:   ghErr == nil,
		Good: "gh on PATH (publishing)",
		Bad:  "gh not found — releases cannot publish; https://cli.github.com",
	})
	if ghErr == nil {
		authErr := exec.Command("gh", "auth", "status").Run()
		checks = append(checks, Check{
			OK:   authErr == nil,
			Good: "gh authenticated",
			Bad:  "gh is installed but not authenticated — run `gh auth login`",
		})
	}
	return checks
}

// FileURL implements Forge — the default-branch blob link used by generated
// release notes.
func (GitHub) FileURL(repo Repo, path string) string {
	return fmt.Sprintf("https://github.com/%s/blob/main/%s", repo, path)
}

// ArchiveTarballURL is the unauthenticated tarball-by-commit endpoint that
// `volt new` fetches templates through — codeload has no API rate limit,
// unlike api.github.com (60 req/hr anonymous, shared per source IP).
func (GitHub) ArchiveTarballURL(repo Repo, commit string) string {
	return fmt.Sprintf("https://codeload.github.com/%s/tar.gz/%s", repo, commit)
}

// UserLogin returns the authenticated user's login, "" when unavailable —
// `volt new` uses it to suggest a module path. Best-effort by design: the
// caller falls back to a placeholder that fails loudly at `go mod tidy`
// rather than silently claiming a namespace.
func (GitHub) UserLogin() string {
	out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ReleaseTags lists the repo's release tag names, newest first, filtered by
// prefix. Self-update needs it because the repo-wide "latest" is useless in
// a monorepo — the newest release may belong to any stream.
func (GitHub) ReleaseTags(repo Repo, prefix string) ([]string, error) {
	out, err := exec.Command("gh", "api", "repos/"+repo.String()+"/releases?per_page=100",
		"--jq", fmt.Sprintf(`[.[].tag_name | select(startswith(%q))]`, prefix)).Output()
	if err != nil {
		return nil, fmt.Errorf("listing releases (is gh installed and authenticated?): %w", err)
	}
	var tags []string
	if err := json.Unmarshal(out, &tags); err != nil {
		return nil, fmt.Errorf("parse release list: %w", err)
	}
	return tags, nil
}

// RawFileURL implements Forge — raw.githubusercontent serves file bytes
// (blob URLs serve an HTML page; curl-ing one into sh is a classic trap).
func (GitHub) RawFileURL(repo Repo, path string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/main/%s", repo, path)
}

// CIFiles implements Forge — the Actions workflow pair.
func (GitHub) CIFiles() []genfiles.File { return genfiles.GitHubWorkflows }

// CloneURL is the anonymous https clone endpoint — scaffold resolves
// template refs through `git ls-remote` on it (no API, no rate limits).
func (GitHub) CloneURL(repo Repo) string {
	return fmt.Sprintf("https://github.com/%s.git", repo)
}
