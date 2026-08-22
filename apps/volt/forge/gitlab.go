// gitlab.go — the GitLab implementation of Forge (FORGE_GITLAB_PLAN):
// glab-CLI publisher (FG-D1), release permanent-link URL shapes (FG-D7).
package forge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/genfiles"
)

// GitLab is the gitlab.com implementation. Zero-value usable; self-hosted
// instances reach it via the `forge:` override (glab itself resolves the
// host from the repo's remote, so no host field is needed until a real
// self-hosted consumer proves otherwise).
type GitLab struct{}

// Name implements Forge.
func (GitLab) Name() string { return "gitlab" }

// ParseRemote implements Forge. Unlike GitHub, GitLab paths NEST
// (group/subgroup/project), so any path with at least owner+name is
// accepted, not exactly two segments.
func (GitLab) ParseRemote(url string) (Repo, bool) {
	var rest string
	switch {
	case strings.HasPrefix(url, "https://gitlab.com/"):
		rest = strings.TrimPrefix(url, "https://gitlab.com/")
	case strings.HasPrefix(url, "git@gitlab.com:"):
		rest = strings.TrimPrefix(url, "git@gitlab.com:")
	default:
		return "", false
	}
	rest = strings.TrimSuffix(rest, ".git")
	rest = strings.Trim(rest, "/")
	if strings.Count(rest, "/") < 1 || strings.Contains(rest, "//") || rest == "" {
		return "", false
	}
	return Repo(rest), true
}

// RepoOf implements Forge. Two rungs mirroring GitHub's: glab (authoritative
// — resolves renames and follows the configured host), then parsing the
// origin URL — so generation works on a repo not yet pushed.
func (g GitLab) RepoOf(dir string) Repo {
	cmd := exec.Command("glab", "repo", "view", "--output", "json")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		var v struct {
			Path string `json:"path_with_namespace"`
		}
		if json.Unmarshal(out, &v) == nil && v.Path != "" {
			return Repo(v.Path)
		}
	}
	raw, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	repo, _ := g.ParseRemote(strings.TrimSpace(string(raw)))
	return repo
}

// Publisher implements Forge — the glab-CLI release driver (FG-D1).
func (GitLab) Publisher(dir string) Publisher { return glabPublisher{dir: dir} }

// AssetURL implements Forge: the release permanent-link shape (FG-D7) —
// stable per tag+filename, redirects without auth on public projects, and
// carries no API rate limit (the property install scripts and skills
// fetching rely on).
func (GitLab) AssetURL(repo Repo, tag, asset string) string {
	return fmt.Sprintf("https://gitlab.com/%s/-/releases/%s/downloads/%s", repo, tag, asset)
}

// LatestAssetURL implements Forge via GitLab's permalink/latest. Same
// monorepo caveat as GitHub's: "latest" is repo-global, any stream.
func (GitLab) LatestAssetURL(repo Repo, asset string) string {
	return fmt.Sprintf("https://gitlab.com/%s/-/releases/permalink/latest/downloads/%s", repo, asset)
}

// FileURL implements Forge — GitLab blob links carry a /-/ separator.
func (GitLab) FileURL(repo Repo, path string) string {
	return fmt.Sprintf("https://gitlab.com/%s/-/blob/main/%s", repo, path)
}

// RawFileURL implements Forge — GitLab's raw endpoint under /-/raw/.
func (GitLab) RawFileURL(repo Repo, path string) string {
	return fmt.Sprintf("https://gitlab.com/%s/-/raw/main/%s", repo, path)
}

// CIFiles implements Forge — one .gitlab-ci.yml (FG-D2).
func (GitLab) CIFiles() []genfiles.File { return genfiles.GitLabCI }

// Doctor implements Forge: publishing needs glab present AND authenticated —
// the exact mirror of the gh probes, for the same mid-release-failure
// reason.
func (GitLab) Doctor() []Check {
	var checks []Check
	_, glabErr := exec.LookPath("glab")
	checks = append(checks, Check{
		OK:   glabErr == nil,
		Good: "glab on PATH (publishing)",
		Bad:  "glab not found — GitLab releases cannot publish; https://gitlab.com/gitlab-org/cli",
	})
	if glabErr == nil {
		authErr := exec.Command("glab", "auth", "status").Run()
		checks = append(checks, Check{
			OK:   authErr == nil,
			Good: "glab authenticated",
			Bad:  "glab is installed but not authenticated — run `glab auth login`",
		})
	}
	return checks
}

// glabPublisher mirrors publish.GH verb-for-verb on the glab CLI. It lives
// here rather than in package publish because the forge ADR grandfathers
// publish/ as the gh driver only — NEW forge drivers belong in forge/.
type glabPublisher struct {
	dir string // repo directory glab runs in (it infers the project from the remote)
}

func (p glabPublisher) run(args ...string) (string, error) {
	cmd := exec.Command("glab", args...)
	cmd.Dir = p.dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("glab %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (p glabPublisher) ReleaseExists(tag string) bool {
	_, err := p.run("release", "view", tag)
	return err == nil
}

// CreateOrUpdate publishes idempotently — same contract as the gh driver:
// edit when the release exists, create otherwise, because a public tag can
// never be deleted and re-pushed. Notes travel by FILE, never argument
// interpolation (the spliced-release-body incident class).
func (p glabPublisher) CreateOrUpdate(tag, title, notesFile string, assets []string) error {
	if p.ReleaseExists(tag) {
		if _, err := p.run("release", "edit", tag, "--name", title, "--notes-file", notesFile); err != nil {
			return err
		}
		if len(assets) > 0 {
			// glab upload replaces same-named links, matching gh --clobber.
			args := append([]string{"release", "upload", tag}, assets...)
			if _, err := p.run(args...); err != nil {
				return err
			}
		}
		return nil
	}
	args := []string{"release", "create", tag, "--name", title, "--notes-file", notesFile}
	args = append(args, assets...)
	_, err := p.run(args...)
	return err
}

func (p glabPublisher) FetchBody(tag string) (string, error) {
	out, err := p.run("release", "view", tag, "--output", "json")
	if err != nil {
		return "", err
	}
	var v struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return "", fmt.Errorf("parse glab release view: %w", err)
	}
	return v.Description, nil
}

func (p glabPublisher) FetchAssetNames(tag string) ([]string, error) {
	out, err := p.run("release", "view", tag, "--output", "json")
	if err != nil {
		return nil, err
	}
	var v struct {
		Assets struct {
			Links []struct {
				Name string `json:"name"`
			} `json:"links"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("parse glab release view: %w", err)
	}
	var names []string
	for _, l := range v.Assets.Links {
		names = append(names, l.Name)
	}
	return names, nil
}
