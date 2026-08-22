package skillcmd

// edge_test.go — the leave-no-stone-unturned suite: every branch the main
// suites did not reach, found by per-function coverage and pinned here.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- get --json / emitJSON (was 0%) --------------------------------------

func TestGetJSONSingleAndAll(t *testing.T) {
	o := liveOpts(t)
	out, err := run(t, o, "get", "lore-core", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload []jsonSkill
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	if len(payload) != 1 || payload[0].Name != "lore-core" || !strings.Contains(payload[0].Content, "core body") {
		t.Fatalf("%+v", payload)
	}
	// --all --json → every skill, content included, still valid JSON.
	out, err = run(t, o, "get", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	payload = nil
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 {
		t.Fatalf("want all 3 skills, got %d", len(payload))
	}
}

// --json --full inlines supporting text files into Content.
func TestGetJSONFullIncludesReferences(t *testing.T) {
	o := liveOpts(t)
	out, err := run(t, o, "get", "lore-core", "--json", "--full")
	if err != nil {
		t.Fatal(err)
	}
	var payload []jsonSkill
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload[0].Content, "all the commands") {
		t.Fatalf("references missing from --json --full content: %q", payload[0].Content)
	}
}

// ---- the real URL fetcher end-to-end ---------------------------------------
// Since the fetcher became template-driven (AssetURLTemplate), the PRODUCTION
// fetcher runs against httptest directly — no test seam needed.

func TestEnsureThroughHTTPFetcher(t *testing.T) {
	bundle, sums := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "---\nname: core\n---\nhttp body\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, bundleAssetName("v1.0.0")):
			_, _ = w.Write(bundle)
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			_, _ = w.Write(sums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	o := opts(t, urlFetcher{tpl: srv.URL + "/khanakia/x/releases/download/{tag}/{asset}"})
	out, err := run(t, o, "get", "core")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "http body") {
		t.Fatalf("%s", out)
	}
}

// The template substitution is the forge contract: GitHub and GitLab shapes
// both render from the same fetcher — tag may itself contain "/" (stream
// tags like notes/v1.0.0) and must land verbatim.
func TestURLTemplateShapes(t *testing.T) {
	gh := urlFetcher{tpl: "https://github.com/o/n/releases/download/{tag}/{asset}"}
	if got, want := gh.url("notes/v1.0.0", "checksums.txt"),
		"https://github.com/o/n/releases/download/notes/v1.0.0/checksums.txt"; got != want {
		t.Fatalf("github shape: %q, want %q", got, want)
	}
	gl := urlFetcher{tpl: "https://gitlab.com/o/n/-/releases/{tag}/downloads/{asset}"}
	if got, want := gl.url("notes/v1.0.0", "skills_v1.0.0.tar.gz"),
		"https://gitlab.com/o/n/-/releases/notes/v1.0.0/downloads/skills_v1.0.0.tar.gz"; got != want {
		t.Fatalf("gitlab shape: %q, want %q", got, want)
	}
}

// First-error shape: a host that cannot resolve fails on the FIRST request,
// and the error carries the bundle URL, not the checksums URL.
func TestFetcherFirstErrorWins(t *testing.T) {
	f := urlFetcher{tpl: "https://github.com/khanakia/definitely-not-a-repo-xyz/releases/download/{tag}/{asset}"}
	_, _, err := f.Fetch("khanakia/definitely-not-a-repo-xyz", "v9.9.9", "v9.9.9")
	if err == nil {
		t.Skip("network reachable and repo unexpectedly resolved — nothing to assert offline")
	}
	if strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("first failure must be the bundle request, got: %v", err)
	}
}

// ---- refresh success path through the command (was 37%) -------------------

func TestRefreshCommandSuccess(t *testing.T) {
	b, s := makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "v1\n"})
	f := &fakeFetcher{bundle: b, sums: s}
	o := opts(t, f)
	if _, err := run(t, o, "list"); err != nil {
		t.Fatal(err)
	}
	f.bundle, f.sums = makeBundle(t, "v1.0.0", map[string]string{"core/SKILL.md": "v1 corrected\n"})
	out, err := run(t, o, "refresh")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "re-fetched v1.0.0") {
		t.Fatalf("%s", out)
	}
	got, _ := run(t, o, "get", "core")
	if !strings.Contains(got, "corrected") {
		t.Fatalf("refresh did not replace content: %s", got)
	}
}

// refresh under a DEV version refuses (live mode) — distinct from env mode.
func TestRefreshRefusesDevVersion(t *testing.T) {
	repoDir := t.TempDir()
	write(t, repoDir, "skills/core/SKILL.md", "---\nname: core\n---\n")
	o := Options{Binary: "demo", Repo: "r", Version: "dev", WorkDir: repoDir, CacheRoot: t.TempDir()}
	o.applyDefaults()
	_, err := run(t, o, "refresh")
	if err == nil || !strings.Contains(err.Error(), "live directory") {
		t.Fatalf("%v", err)
	}
}

// ---- untarStrip1 edge cases (was 64%) -------------------------------------

func tarball(t *testing.T, entries func(*tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUntarStrip1EdgeCases(t *testing.T) {
	// Not gzip at all → error, not panic.
	if err := untarStrip1(bytes.NewReader([]byte("not gzip")), t.TempDir()); err == nil {
		t.Fatal("garbage input must error")
	}
	// Truncated gzip stream → error.
	b, _ := makeBundle(t, "v1", map[string]string{"a/SKILL.md": "x"})
	if err := untarStrip1(bytes.NewReader(b[:len(b)/2]), t.TempDir()); err == nil {
		t.Fatal("truncated stream must error")
	}
	// Wrapper-only entries (no second path segment) are skipped, dirs made,
	// symlinks and other special types ignored rather than extracted.
	dest := t.TempDir()
	tb := tarball(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(&tar.Header{Name: "skills/", Typeflag: tar.TypeDir, Mode: 0o755})
		_ = tw.WriteHeader(&tar.Header{Name: "skills/sub/", Typeflag: tar.TypeDir, Mode: 0o755})
		_ = tw.WriteHeader(&tar.Header{Name: "skills/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
		_ = tw.WriteHeader(&tar.Header{Name: "skills/sub/f.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2})
		_, _ = tw.Write([]byte("ok"))
	})
	if err := untarStrip1(bytes.NewReader(tb), dest); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(dest, "sub", "f.md")); err != nil || string(raw) != "ok" {
		t.Fatalf("nested file lost: %v %q", err, raw)
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); !os.IsNotExist(err) {
		t.Fatal("symlinks must NOT be extracted — a bundle must not be able to write through a link")
	}
}

// ---- findLiveDir from the real working directory (was 63%) ----------------

func TestFindLiveDirDefaultsToCwd(t *testing.T) {
	repoDir := t.TempDir()
	write(t, repoDir, "skills/core/SKILL.md", "x")
	old, _ := os.Getwd()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	dir, err := findLiveDir("")
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	want, _ := filepath.EvalSymlinks(filepath.Join(repoDir, "skills"))
	if resolved != want {
		t.Fatalf("got %q want %q", resolved, want)
	}
}

// ---- version command, human output (was 75%) ------------------------------

func TestVersionCommandHuman(t *testing.T) {
	out, err := run(t, liveOpts(t), "version")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"demo v1.0.0", "skills_hash: ", "source: env:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

// ---- check --json + error paths (was 74%) ---------------------------------

func TestCheckJSONOutput(t *testing.T) {
	o := liveOpts(t)
	root := os.Getenv(o.Env)
	installed := filepath.Join(t.TempDir(), "lore-core")
	if err := os.CopyFS(installed, os.DirFS(filepath.Join(root, "lore-core"))); err != nil {
		t.Fatal(err)
	}
	write(t, installed, "extra.md", "mine")
	out, err := run(t, o, "check", installed, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res CheckResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Current || len(res.Extras) != 1 || res.Extras[0] != "extra.md" {
		t.Fatalf("%+v", res)
	}
}

// check against a skill the binary does not have → the unknown-name error.
func TestCheckUnknownSkillErrors(t *testing.T) {
	o := liveOpts(t)
	installed := filepath.Join(t.TempDir(), "ghost-skill")
	write(t, installed, "SKILL.md", "---\nname: ghost-skill\n---\n")
	_, err := run(t, o, "check", installed)
	if err == nil || !strings.Contains(err.Error(), "ghost-skill") {
		t.Fatalf("%v", err)
	}
}

// check against a single-file skill → the explicit redirect error.
func TestCheckSingleFileSkillRedirects(t *testing.T) {
	o := liveOpts(t)
	installed := filepath.Join(t.TempDir(), "quickref")
	write(t, installed, "SKILL.md", "---\nname: quickref\n---\n")
	_, err := run(t, o, "check", installed)
	if err == nil || !strings.Contains(err.Error(), "skills get quickref") {
		t.Fatalf("single-file check must redirect to get: %v", err)
	}
}

// installed dir missing SKILL.md entirely → dir-name fallback still finds
// the reference, and the comparison reports the missing file as stale.
func TestCheckMissingSkillMDFallsBackToDirName(t *testing.T) {
	o := liveOpts(t)
	installed := filepath.Join(t.TempDir(), "lore-core")
	write(t, installed, "references/commands.md", "all the commands\n")
	out, err := run(t, o, "check", installed)
	if err == nil {
		t.Fatal("missing SKILL.md must be stale")
	}
	if !strings.Contains(out, "SKILL.md") {
		t.Fatalf("must name the missing file:\n%s", out)
	}
}

// ---- list edge: empty skills dir ------------------------------------------

func TestListEmptySkillsDir(t *testing.T) {
	o := Options{Binary: "demo", Repo: "khanakia/demo", Version: "v1.0.0", CacheRoot: t.TempDir()}
	o.applyDefaults()
	t.Setenv(o.Env, t.TempDir()) // exists, but holds nothing
	out, err := run(t, o, "list")
	if err != nil {
		t.Fatal(err)
	}
	// Empty is a legal state (a repo before its first skill) — the install
	// hint still prints; nothing else does.
	if !strings.Contains(out, "npx skills add") {
		t.Fatalf("%s", out)
	}
}

// env override pointing at a MISSING dir: the serve fails with the real
// filesystem error, not a silent empty list.
func TestEnvOverrideMissingDirErrors(t *testing.T) {
	o := Options{Binary: "demo", Repo: "r", Version: "v1.0.0", CacheRoot: t.TempDir()}
	o.applyDefaults()
	t.Setenv(o.Env, filepath.Join(t.TempDir(), "nope"))
	_, err := run(t, o, "list")
	if err == nil {
		t.Fatal("missing override dir must error, never serve empty")
	}
}

// ---- isBinary boundary ----------------------------------------------------

func TestIsBinaryBoundaries(t *testing.T) {
	if isBinary([]byte("plain text, no nulls")) {
		t.Fatal("text misclassified")
	}
	if !isBinary([]byte{'a', 0x00, 'b'}) {
		t.Fatal("NUL not detected")
	}
	// NUL beyond the 1KB probe window → treated as text (documented probe).
	big := append(bytes.Repeat([]byte("a"), 1024), 0x00)
	if isBinary(big) {
		t.Fatal("probe window is the first KB by contract")
	}
	if isBinary(nil) {
		t.Fatal("empty must be text")
	}
}

// ---- bare `skills` root vs list parity ------------------------------------

func TestBareAndListIdentical(t *testing.T) {
	o := liveOpts(t)
	bare, err := run(t, o)
	if err != nil {
		t.Fatal(err)
	}
	list, err := run(t, o, "list")
	if err != nil {
		t.Fatal(err)
	}
	if bare != list {
		t.Fatalf("bare skills and skills list must be identical:\n%q\n%q", bare, list)
	}
}

// Compatibility promise pinned: a wiring generated BEFORE forges existed
// (no AssetURLTemplate) must default to the GitHub shape derived from Repo —
// every already-shipped binary fetches exactly as it always did.
func TestDefaultAssetURLTemplateIsGitHubShape(t *testing.T) {
	o := Options{Binary: "notes", Repo: "khanakia/demo", Version: "v1.0.0"}
	o.applyDefaults()
	want := "https://github.com/khanakia/demo/releases/download/{tag}/{asset}"
	if o.AssetURLTemplate != want {
		t.Fatalf("default template = %q, want %q", o.AssetURLTemplate, want)
	}
	f, ok := o.Fetcher.(urlFetcher)
	if !ok {
		t.Fatalf("default fetcher must be the urlFetcher, got %T", o.Fetcher)
	}
	if got := f.url("notes/v1.0.0", "checksums.txt"); got != "https://github.com/khanakia/demo/releases/download/notes/v1.0.0/checksums.txt" {
		t.Fatalf("default fetch URL = %q", got)
	}
}
