// Package scaffold implements `volt new`: fetch a template repo at a pinned
// ref, overlay _base + variant, render, and stamp the generated project
// (spec, "Templates and variants" + ADR-R13).
//
// Templates are NEVER embedded in the binary — they change far more often
// than volt does. The default ref is a tag pinned per volt release, never a
// branch, so a bad template commit cannot break scaffolding fleet-wide.
package scaffold

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"text/template"

	"github.com/khanakia/voltkit/apps/volt/forge"

	"gopkg.in/yaml.v3"
)

// DefaultRepo and DefaultRef pin where `volt new cli` scaffolds from. The
// ref advances with volt releases — a deliberate act, per the spec.
const (
	DefaultRepo = "khanakia/volt-cli"
	DefaultRef  = "v0.1.0"
)

// tmplExt marks files whose CONTENT is rendered; the suffix is stripped on
// write. Everything else copies verbatim.
const tmplExt = ".tmpl"

// Vars feeds template rendering. Delimiters are [[ ]] (same reason as
// genfiles): rendered files legitimately contain Go's {{ }} — the .volt.yml
// ldflags templates — which must pass through untouched.
type Vars struct {
	Name           string // binary/project name
	Module         string // Go module path
	Variant        string
	Ref            string
	TemplateRepo   string
	TemplateCommit string
	VoltVersion    string
	At             string // RFC3339 generation time
}

// Meta is volt-templates.yml at the template repo root.
type Meta struct {
	Version  int    `yaml:"version"`
	Default  string `yaml:"default"`
	Variants map[string]struct {
		Description string `yaml:"description"`
	} `yaml:"variants"`
}

// Fetch downloads repo@ref into the local cache (keyed by ref, so re-runs
// are offline) and returns the extracted root plus the resolved commit.
func Fetch(repo, ref string) (dir string, commit string, err error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", "", err
	}
	dest := filepath.Join(cacheRoot, "volt", "templates", repo, ref)
	commitFile := filepath.Join(dest, ".volt-template-commit")
	if raw, err := os.ReadFile(commitFile); err == nil {
		return dest, strings.TrimSpace(string(raw)), nil // cache hit
	}

	// Resolve the ref to a commit FIRST — recorded in the generation stamp
	// so a moving tag cannot make two generations claim to be identical.
	commit, err = resolveCommit(repo, ref)
	if err != nil {
		return "", "", err
	}
	// forge.GitHub constructed, not detected: template repos are addressed
	// as owner/name on GitHub by the --repo contract.
	url := (forge.GitHub{}).ArchiveTarballURL(forge.Repo(repo), commit)
	resp, err := http.Get(url)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch template %s@%s: HTTP %d (private repo? volt new needs it public or a cached copy)", repo, ref, resp.StatusCode)
	}
	if err := untarStrip1(resp.Body, dest); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(commitFile, []byte(commit+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return dest, commit, nil
}

// shaRE matches a full 40-hex commit — passed through unresolved, because
// `volt upgrade` fetches templates by the RECORDED commit from the
// generation stamp: a tag that moved since scaffolding must not change what
// "the old template" means.
var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// resolveCommit asks the remote which commit a ref names — plain git
// ls-remote, no API, no rate limits, works for tags and branches.
func resolveCommit(repo, ref string) (string, error) {
	if shaRE.MatchString(ref) {
		return ref, nil
	}
	out, err := exec.Command("git", "ls-remote", (forge.GitHub{}).CloneURL(forge.Repo(repo)),
		"refs/tags/"+ref+"^{}", "refs/tags/"+ref, "refs/heads/"+ref).Output()
	if err != nil {
		return "", fmt.Errorf("resolving %s@%s: %w", repo, ref, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] == "" {
		return "", fmt.Errorf("%s has no tag or branch named %q", repo, ref)
	}
	// Peeled tag (^{}) first when present — that is the commit, not the tag object.
	return strings.Fields(lines[0])[0], nil
}

// LoadMeta reads volt-templates.yml from a fetched template root.
func LoadMeta(templateRoot string) (Meta, error) {
	var m Meta
	raw, err := os.ReadFile(filepath.Join(templateRoot, "volt-templates.yml"))
	if err != nil {
		return m, fmt.Errorf("template repo has no volt-templates.yml: %w", err)
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	return m, nil
}

// Generate renders _base then the variant overlay into destDir. destDir must
// not already exist — scaffolding never overwrites anything.
func Generate(templateRoot, variant, destDir string, v Vars) ([]string, error) {
	if _, err := os.Stat(destDir); err == nil {
		return nil, fmt.Errorf("%s already exists — volt new never overwrites", destDir)
	}
	var written []string
	for _, layer := range []string{"_base", variant} {
		src := filepath.Join(templateRoot, "templates", layer)
		if _, err := os.Stat(src); err != nil {
			return nil, fmt.Errorf("template variant %q: %s missing in the template repo", variant, layer)
		}
		files, err := renderLayer(src, destDir, v)
		if err != nil {
			return nil, err
		}
		written = append(written, files...)
	}
	sort.Strings(written)
	return dedupe(written), nil
}

// renderLayer walks one layer; later layers overwrite earlier ones — that IS
// the overlay semantic (a variant may replace a _base file wholesale).
func renderLayer(src, dest string, v Vars) ([]string, error) {
	var written []string
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := raw
		if strings.HasSuffix(rel, tmplExt) {
			rel = strings.TrimSuffix(rel, tmplExt)
			t, err := template.New(rel).Delims("[[", "]]").Option("missingkey=error").Parse(string(raw))
			if err != nil {
				return fmt.Errorf("template %s: %w", rel, err)
			}
			var b bytes.Buffer
			if err := t.Execute(&b, v); err != nil {
				return fmt.Errorf("template %s: %w", rel, err)
			}
			out = b.Bytes()
		}
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		written = append(written, rel)
		return os.WriteFile(target, out, 0o644)
	})
	return written, err
}

// untarStrip1 extracts a GitHub codeload tarball, dropping the single
// "<repo>-<sha>/" wrapper directory GitHub adds.
func untarStrip1(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		parts := strings.SplitN(filepath.ToSlash(hdr.Name), "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(parts[1]))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
