// Package genfiles writes volt's generated files — workflow stubs and
// install scripts — with hash-guarded headers (ADR-R11).
//
// The guard exists because `DO NOT EDIT` alone provably fails: in
// solverhood/sync_go a hand-fix to a generated workflow was silently deleted
// by the next regeneration (2026-08-19). Every file volt writes carries a
// body hash; regeneration REFUSES to overwrite a body whose hash no longer
// matches, printing the diff instead.
package genfiles

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates/*
var templates embed.FS

// hashMarker introduces the body hash inside the header. The space matters:
// scanning looks for the marker at a comment position on its own line.
const hashMarker = "volt:hash "

// hashLen is how many hex chars of the sha256 land in the header — enough to
// make accidental collision irrelevant, short enough to read.
const hashLen = 12

// File is one generated file.
type File struct {
	// RelPath is where it lands, relative to the repo root.
	RelPath string
	// Template is the name under templates/ it renders from.
	Template string
	// Comment is the per-line comment prefix for the header ("#" for
	// YAML/shell/PowerShell — every current target uses hash comments).
	Comment string
	// CLIOnly files are generated only when the repo ROOT is a single CLI
	// (package main at the root) — see the Registry comment.
	CLIOnly bool
}

// The generated set is split by OWNER (FORGE_GITLAB_PLAN FG-D2). Files volt
// must never touch — CHANGELOG.md, README.md, .volt.yml — are simply absent
// from every list; their protection is structural, not a check.
//
// GitHubWorkflows / GitLabCI are each forge's CI contribution — `volt gen`
// asks the DETECTED forge (Forge.CIFiles) which set applies, so a GitLab
// repo never grows a .github/ directory and vice versa. The hash-guard
// engine below is forge-agnostic and shared.
var GitHubWorkflows = []File{
	{RelPath: ".github/workflows/ci.yml", Template: "ci.yml", Comment: "#"},
	{RelPath: ".github/workflows/release.yml", Template: "release.yml", Comment: "#"},
}

// GitLabCI is GitLab's generated set: one .gitlab-ci.yml holding both the ci
// and (manual-only, ADR-R14) release jobs — GitLab has no per-workflow
// files.
var GitLabCI = []File{
	{RelPath: ".gitlab-ci.yml", Template: "gitlab-ci.yml", Comment: "#"},
}

// InstallScripts are forge-SHARED: the same script works on any forge
// because the download URL bases arrive as template variables (FG-D4)
// rendered through the forge's URL shapes at gen time.
//
// CLIOnly: install scripts install A binary from the repo root's release. A
// library has no binary, and a multi-CLI monorepo has no single one —
// "latest" is repo-wide, so it is whichever CLI released last. Both cases
// skip these files, loudly.
var InstallScripts = []File{
	{RelPath: "install.sh", Template: "install.sh", Comment: "#", CLIOnly: true},
	{RelPath: "install.ps1", Template: "install.ps1", Comment: "#", CLIOnly: true},
}

// Vars parameterise the templates. The URL fields carry the forge's shapes
// into the shared install scripts (FG-D4) — templates never hardcode a host.
type Vars struct {
	Repo    string // "owner/name" (may nest on GitLab: group/sub/proj)
	Binary  string
	Version string // volt's own version, recorded in the header
	// DownloadBase is the versioned download URL base with a literal
	// ${VERSION} the SCRIPT expands at run time, no trailing slash — e.g.
	// "https://github.com/o/n/releases/download/${VERSION}".
	DownloadBase string
	// LatestBase is the latest-release download base, no trailing slash;
	// "" when the forge has no latest redirect — the script then requires
	// an explicit version, loudly.
	LatestBase string
	// DownloadBasePS mirrors DownloadBase with PowerShell's $Version
	// variable syntax — the two scripts interpolate differently, so each
	// gets its own literal.
	DownloadBasePS string
	// RawScriptURL / RawScriptURLPS are the curl-able raw URLs of each
	// script itself, for the "curl | sh" / "irm | iex" usage hints.
	RawScriptURL   string
	RawScriptURLPS string
}

// Outcome of generating one file.
type Outcome int

const (
	Written   Outcome = iota // file was absent, or hash matched — (re)written
	Unchanged                // rendered bytes identical to what is on disk
	Refused                  // body was hand-edited (or header missing) and --force not given
)

// Result reports one file's generation.
type Result struct {
	RelPath string
	Outcome Outcome
	// Diff is a unified-ish summary present when Refused, so the operator
	// sees WHAT was hand-changed before deciding to --force.
	Diff string
}

// Generate renders f into rootDir, honouring the hash guard:
//
//	absent            → write
//	hash matches body → rewrite (regeneration is safe)
//	hash differs      → refuse + diff (a hand edit would be destroyed)
//	no header at all  → refuse (an unmarked file is never assumed ours)
//
// force overwrites in the last two cases; the caller owns offering --backup.
func Generate(rootDir string, f File, v Vars, force bool) (Result, error) {
	return generateWith(rootDir, f, v, nil, force)
}

// generateWith is Generate plus template-local extras (fields beyond Vars a
// single template needs, e.g. the skills wiring's Tag).
func generateWith(rootDir string, f File, v Vars, extras map[string]string, force bool) (Result, error) {
	res := Result{RelPath: f.RelPath}
	rendered, err := render(f, v, extras)
	if err != nil {
		return res, err
	}
	dest := filepath.Join(rootDir, f.RelPath)

	existing, err := os.ReadFile(dest)
	switch {
	case os.IsNotExist(err):
		// absent → write
	case err != nil:
		return res, err
	case bytes.Equal(existing, rendered):
		res.Outcome = Unchanged
		return res, nil
	case !force:
		if reason, edited := handEdited(existing); edited {
			res.Outcome = Refused
			res.Diff = fmt.Sprintf("%s: %s\n%s", f.RelPath, reason, diffSummary(existing, rendered))
			return res, nil
		}
		// hash matches → the on-disk body is exactly what some volt version
		// generated. But WHICH version matters: an older released volt
		// regenerating a newer volt's files silently reverts fixes (hit for
		// real: released v0.1.0 reverted install.sh's macOS fix in
		// volt-demo-cli, 2026-08-22). Downgrades refuse; --force overrides.
		if reason := downgradeRisk(existing, v.Version); reason != "" {
			res.Outcome = Refused
			res.Diff = fmt.Sprintf("%s: %s\n", f.RelPath, reason)
			return res, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return res, err
	}
	mode := os.FileMode(0o644)
	if strings.HasSuffix(f.RelPath, ".sh") {
		mode = 0o755
	}
	if err := os.WriteFile(dest, rendered, mode); err != nil {
		return res, err
	}
	res.Outcome = Written
	return res, nil
}

// render produces header + body. The hash covers the BODY only, so bumping
// volt's version in the header does not read as a hand edit.
func render(f File, v Vars, extras map[string]string) ([]byte, error) {
	raw, err := templates.ReadFile("templates/" + f.Template)
	if err != nil {
		return nil, err
	}
	body, err := renderBytes(string(raw), v, extras)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", f.Template, err)
	}
	// Delims are [[ ]] because the workflow templates contain GitHub
	// Actions ${{ }} expressions, which must pass through VERBATIM — Go's
	// default {{ }} would try to parse them.
	c := f.Comment
	var out bytes.Buffer
	// Shell scripts must keep the shebang as line 1.
	rest := body
	if bytes.HasPrefix(rest, []byte("#!")) {
		nl := bytes.IndexByte(rest, '\n')
		out.Write(rest[:nl+1])
		rest = rest[nl+1:]
	}
	fmt.Fprintf(&out, "%s Code generated by volt %s — DO NOT EDIT.\n", c, v.Version)
	fmt.Fprintf(&out, "%s\n", c)
	fmt.Fprintf(&out, "%s Regenerate:  volt gen\n", c)
	fmt.Fprintf(&out, "%s To change:   edit .volt.yml, or the template in volt itself — edits here are lost.\n", c)
	sum := sha256.Sum256(rest)
	fmt.Fprintf(&out, "%s %s%x\n", c, hashMarker, sum[:hashLen/2])
	out.Write(rest)
	return out.Bytes(), nil
}

// renderBytes renders template text against Vars plus optional extras, with
// the [[ ]] delimiters (GHA's ${{ }} and Go's {{ }} both pass through).
func renderBytes(text string, v Vars, extras map[string]string) ([]byte, error) {
	data := map[string]string{
		"Repo": v.Repo, "Binary": v.Binary, "Version": v.Version,
		"DownloadBase": v.DownloadBase, "DownloadBasePS": v.DownloadBasePS,
		"LatestBase": v.LatestBase, "RawScriptURL": v.RawScriptURL,
		"RawScriptURLPS": v.RawScriptURLPS,
	}
	for k, val := range extras {
		data[k] = val
	}
	t, err := template.New("t").Option("missingkey=error").Delims("[[", "]]").Parse(text)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// handEdited inspects an existing file: (reason, true) when overwriting it
// would destroy human work.
func handEdited(existing []byte) (string, bool) {
	lines := bytes.Split(existing, []byte("\n"))
	var declared string
	bodyStart := -1
	for i, line := range lines {
		if idx := bytes.Index(line, []byte(hashMarker)); idx >= 0 {
			declared = string(bytes.TrimSpace(line[idx+len(hashMarker):]))
			bodyStart = i + 1
			break
		}
		if i > 10 { // headers are short; a marker below line 10 is not ours
			break
		}
	}
	if bodyStart < 0 {
		return "no volt:hash header — not a file volt generated (or the header was removed); refusing to overwrite", true
	}
	body := bytes.Join(lines[bodyStart:], []byte("\n"))
	sum := sha256.Sum256(body)
	actual := fmt.Sprintf("%x", sum[:hashLen/2])
	if actual != declared {
		return "body was edited after generation (hash mismatch)", true
	}
	return "", false
}

// diffSummary is a compact both-sides summary — enough to decide, without
// shipping a diff library.
func diffSummary(existing, rendered []byte) string {
	e, r := bytes.Split(existing, []byte("\n")), bytes.Split(rendered, []byte("\n"))
	var b strings.Builder
	max := len(e)
	if len(r) > max {
		max = len(r)
	}
	shown := 0
	for i := 0; i < max && shown < 20; i++ {
		var el, rl []byte
		if i < len(e) {
			el = e[i]
		}
		if i < len(r) {
			rl = r[i]
		}
		if !bytes.Equal(el, rl) {
			fmt.Fprintf(&b, "  -%s\n  +%s\n", el, rl)
			shown++
		}
	}
	if shown == 20 {
		b.WriteString("  … (more differences)\n")
	}
	return b.String()
}

// genVersionRE captures the generator version from the header line
// "Code generated by volt <version> — DO NOT EDIT."
var genVersionRE = regexp.MustCompile(`Code generated by volt (\S+)`)

// releaseRE matches a released version; anything else (v0.0.0-dev.abc, "dev")
// is a working-tree build whose ordering against releases is unknowable.
var releaseRE = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// downgradeRisk reports a non-empty reason when regenerating with the
// current volt would move the file BACKWARD:
//
//	current is a dev build      → never a downgrade: the developer's working
//	                              tree is by definition the newest code
//	on-disk generator is a dev  → refuse: a release cannot know whether that
//	build                         working tree was newer than itself
//	both are releases           → refuse when on-disk > current
func downgradeRisk(existing []byte, current string) string {
	m := genVersionRE.FindSubmatch(existing)
	if m == nil {
		return "" // pre-guard file with no version in the header; hash already vouched for it
	}
	onDisk := string(m[1])
	if !releaseRE.MatchString(current) {
		return "" // dev build regenerating: allowed
	}
	if !releaseRE.MatchString(onDisk) {
		return fmt.Sprintf("generated by a dev build (%s), and this volt is the %s release — regenerating might revert newer work; run gen with a current dev build, or --force", onDisk, current)
	}
	if semverLess(current, onDisk) {
		return fmt.Sprintf("generated by volt %s, but this volt is older (%s) — regenerating would downgrade the file; run `volt update` first, or --force", onDisk, current)
	}
	return ""
}

// semverLess reports a < b for released vX.Y.Z versions.
func semverLess(a, b string) bool {
	pa, pb := releaseRE.FindStringSubmatch(a), releaseRE.FindStringSubmatch(b)
	for i := 1; i <= 3; i++ {
		na, _ := strconv.Atoi(pa[i])
		nb, _ := strconv.Atoi(pb[i])
		if na != nb {
			return na < nb
		}
	}
	return false
}
