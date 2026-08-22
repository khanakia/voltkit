// Package release orchestrates a release end-to-end — the heart of
// `volt release` (spec, "volt release — the human path" + ADR-R10).
//
// The step ORDER is the design: everything reversible happens before anything
// permanent. tests → reserve tag → build → publish → verify. A failure after
// the tag exists leaves an orphan tag, which --from-tag re-runs idempotently;
// the alternative (build first, tag last) would rebuild under a different
// version whenever the push loses a race.
package release

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/archive"
	"github.com/khanakia/voltkit/apps/volt/changelog"
	"github.com/khanakia/voltkit/apps/volt/detect"
	"github.com/khanakia/voltkit/apps/volt/gitx"
	"github.com/khanakia/voltkit/apps/volt/gobuild"
	"github.com/khanakia/voltkit/apps/volt/platform"
	"github.com/khanakia/voltkit/apps/volt/publish"
	"github.com/khanakia/voltkit/apps/volt/relname"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"
	"os"
	"path/filepath"
)

// Remote is the git remote releases push to. A constant until someone needs
// otherwise; multi-remote release flows are not a real use case yet.
const Remote = "origin"

// SnapshotPrefix names snapshot pseudo-versions: valid semver even with no
// tag history (the dispatch-snapshot path can run off any branch).
const SnapshotPrefix = "v0.0.0-snapshot-"

// Options configures one release run.
type Options struct {
	Root    string // repo root (where git commands run)
	Dir     string // repo-relative directory to release; "." for the root
	Version string // explicit vX.Y.Z; empty only with FromTag or Snapshot
	FromTag string // existing tag to (re)publish — the CI path
	// Snapshot builds everything and publishes nothing; version becomes
	// v0.0.0-snapshot-<shortcommit>. No tag is created.
	Snapshot bool
	// SkipTests is for --from-tag re-runs where the tag's commit already
	// passed; the human path never sets it.
	SkipTests bool
	// FromArtifacts publishes pre-built archives from DistDir instead of
	// building — the native-runner-matrix assembly path (ADR-R09). Refuses
	// an incomplete platform set: a partial release cannot be corrected.
	FromArtifacts bool
	// Strict turns skipped channels (missing credential/config) into errors.
	Strict bool
	// Brew configures the Homebrew channel; zero value disables it.
	Brew      publish.BrewConfig
	Publisher publish.Publisher
	// WarmProxy and PushFormula default to the real implementations in
	// NewDefaultHooks; tests substitute fakes. Nil WarmProxy disables the
	// proxy step entirely.
	WarmProxy    func(module, version string) error
	PushFormula  func(tap, binary, formula, message string) error
	Repo         string    // "owner/name" for the brew formula; "" is tolerated
	ChangelogURL string    // forge-built link to CHANGELOG.md for generated notes; "" omits it
	DistDir      string    // override for tests; empty → ./dist
	Log          io.Writer // progress lines; nil → io.Discard
}

// Result is what a release produced.
type Result struct {
	Tag      string
	Kind     detect.Kind
	Assets   []string
	Notes    string
	Warnings []string
}

// Run executes the release. See the package comment for why the order is
// fixed.
func Run(o Options) (Result, error) {
	var res Result
	log := o.Log
	if log == nil {
		log = io.Discard
	}

	// --- resolve what is being released -------------------------------
	if o.FromTag != "" {
		r, err := relname.Resolve(o.Root, o.FromTag)
		if err != nil {
			return res, err
		}
		o.Dir, o.Version = r.Dir, r.Version
		res.Tag, res.Kind = o.FromTag, r.Kind
		_, _ = fmt.Fprintf(log, "resolved %s → %s (%s)\n", o.FromTag, r.Dir, r.Kind)
	}

	// Normalize the directory once: a user-typed "./pkg/textutil" must not
	// leak its "./" into anything user-visible (it reached a release title).
	o.Dir = filepath.ToSlash(filepath.Clean(o.Dir))
	absDir := filepath.Join(o.Root, o.Dir)
	if voltcfg.IsInternal(o.Root, o.Dir) {
		return res, fmt.Errorf("%s is marked internal (`internal: true` in %s) — it is never released; remove the marker if that changed", o.Dir, voltcfg.FileName)
	}
	cfg, err := voltcfg.Load(absDir)
	if err != nil {
		return res, err
	}
	if res.Kind == "" {
		res.Kind, err = detect.Dir(absDir)
		if err != nil {
			return res, err
		}
	}

	// --- step 1: refuse a dirty tree ----------------------------------
	// Snapshot builds are exempt: they publish nothing and exist precisely
	// for trying uncommitted work.
	if !o.Snapshot {
		dirty, err := gitx.IsDirty(o.Root)
		if err != nil {
			return res, err
		}
		if dirty {
			return res, fmt.Errorf("working tree is dirty — a release tag must name a commit containing exactly what was built (commit or stash first)")
		}
	}

	// --- snapshot short-circuit ---------------------------------------
	if o.Snapshot {
		head, _ := gitx.Head(o.Root)
		short := head
		if len(short) > 7 {
			short = short[:7]
		}
		o.Version = SnapshotPrefix + short
		_, _ = fmt.Fprintf(log, "snapshot build %s — publishing nothing\n", o.Version)
		return buildOnly(o, cfg, res, log)
	}

	// --- step 2: tests before anything permanent ----------------------
	// For a library the tag IS the release: the proxy caches a fetched
	// version forever, so a tag that cannot pass its tests must never exist.
	if !o.SkipTests {
		_, _ = fmt.Fprintf(log, "testing %s\n", o.Dir)
		if out, err := runGo(absDir, "test", "./..."); err != nil {
			return res, fmt.Errorf("tests failed — nothing was tagged:\n%s", out)
		}
	}

	// --- step 3: compose the tag --------------------------------------
	// Composed BEFORE the pre_release hook so the hook sees VOLT_TAG —
	// a gate that logs or checks "what is about to be tagged" needs the
	// name even though nothing is created yet.
	if res.Tag == "" {
		res.Tag, err = relname.Compose(res.Kind, o.Dir, cfg.Binary, o.Version)
		if err != nil {
			return res, err
		}
	}

	// --- step 3.5: the project's own gate ------------------------------
	// pre_release runs after volt's checks and BEFORE anything permanent:
	// a failing hook aborts with no tag, no release, nothing to clean up.
	// Skipped on --from-tag (the tag already exists; there is nothing left
	// for a gate to prevent).
	if o.FromTag == "" && cfg.Hooks.PreRelease != "" {
		if err := runHook("pre_release", cfg.Hooks.PreRelease, o, res, cfg, log); err != nil {
			return res, fmt.Errorf("pre_release hook refused the release — nothing was tagged: %w", err)
		}
	}

	// --- step 4: reserve the tag (unless it already exists — FromTag) --
	if !gitx.TagExists(o.Root, res.Tag) {
		if err := reserve(o.Root, res.Tag, log); err != nil {
			return res, err
		}
	} else {
		_, _ = fmt.Fprintf(log, "tag %s already exists — republishing it\n", res.Tag)
	}

	// --- step 5: release notes ----------------------------------------
	res.Notes = changelog.Notes(absDir, res.Tag, strings.TrimPrefix(o.Version, "v"), o.ChangelogURL)

	// --- step 6: build (CLI only) + publish ---------------------------
	if res.Kind == detect.KindCLI {
		if o.FromArtifacts {
			assets, err := collectArtifacts(o, cfg)
			if err != nil {
				return res, err
			}
			res.Assets = assets
		} else {
			b, err := gobuild.Run(gobuild.Options{Dir: absDir, Version: o.Version, DistDir: o.DistDir, Log: log}, cfg)
			if err != nil {
				return res, fmt.Errorf("build failed — tag %s exists but is unpublished; fix and re-run `volt release --from-tag %s`: %w", res.Tag, res.Tag, err)
			}
			res.Warnings = append(res.Warnings, b.Warnings...)
			dist := o.DistDir
			if dist == "" {
				dist = gobuild.DistDirName
			}
			for _, a := range b.Assets {
				res.Assets = append(res.Assets, filepath.Join(dist, a))
			}
			res.Assets = append(res.Assets, b.Checksums)
		}
	}

	// Skills bundle (SKILLCMD_SPEC): a CLI whose released directory (or the
	// repo root) carries a managed skills/ ships skills_<version>.tar.gz —
	// load-bearing for the skillcmd cache, and the canonical bundle for
	// registries. Attach failures fail the release: a version published
	// without its bundle errors at every consumer's first use.
	if res.Kind == detect.KindCLI {
		if bundle, err := buildSkillsBundle(o, cfg, absDir); err != nil {
			return res, fmt.Errorf("skills bundle: %w", err)
		} else if bundle != "" {
			res.Assets = append(res.Assets, bundle)
			_, _ = fmt.Fprintf(log, "skills bundle %s\n", filepath.Base(bundle))
			// checksums.txt was written by the BUILD, before the bundle
			// existed — and skillcmd verifies the bundle against it, so an
			// uncovered bundle makes every consumer fetch refuse ("no
			// entry"). Recompute over the complete asset set. (Caught by a
			// unit test before it could break a live fetch, 2026-08-22.)
			if err := recomputeChecksums(o, &res); err != nil {
				return res, fmt.Errorf("checksums over the final asset set: %w", err)
			}
		}
	}

	notesFile, err := publish.WriteNotesFile(res.Notes)
	if err != nil {
		return res, err
	}
	defer func() { _ = os.Remove(notesFile) }() // temp-file cleanup is best-effort
	// The release title IS the tag — the convention of every big multi-module
	// repo (aws-sdk-go-v2, google-cloud-go, opentelemetry-go). A previous
	// hand-rolled title split the tag at the FIRST slash and leaked the raw
	// "./dir" argument, producing "./pkg/textutil textutil/v0.1.0".
	if err := o.Publisher.CreateOrUpdate(res.Tag, res.Tag, notesFile, res.Assets); err != nil {
		return res, fmt.Errorf("publish failed — tag %s exists; re-run `volt release --from-tag %s`: %w", res.Tag, res.Tag, err)
	}

	// --- step 7: verify the artifact, not the exit code ---------------
	if problems := publish.Verify(o.Publisher, res.Tag, res.Notes, res.Assets); len(problems) > 0 {
		return res, fmt.Errorf("published but verification FAILED:\n  %s", strings.Join(problems, "\n  "))
	}

	// Library: warm the public proxy — until it resolves, the release has
	// not happened for consumers. Failure is a warning (private repos and
	// proxy lag are expected), promoted to an error only under --strict.
	if res.Kind == detect.KindLibrary && o.WarmProxy != nil {
		mod, err := publish.ModulePath(absDir)
		if err != nil {
			// Loud, never silent: an unresolvable module means the proxy
			// step cannot even be attempted.
			res.Warnings = append(res.Warnings, fmt.Sprintf("proxy warm-up skipped: %v", err))
		} else if err := o.WarmProxy(mod, o.Version); err != nil {
			msg := fmt.Sprintf("proxy warm-up: %v (private repo or proxy lag — consumers may see a delay)", err)
			if o.Strict {
				return res, fmt.Errorf("%s", msg)
			}
			res.Warnings = append(res.Warnings, msg)
		} else {
			_, _ = fmt.Fprintf(log, "proxy resolves %s@%s\n", mod, o.Version)
		}
	}

	// Homebrew: credential-gated — configured tap + successful push, or a
	// loud skip. Never fails the release except under --strict.
	if res.Kind == detect.KindCLI && o.Brew.Tap != "" {
		if o.PushFormula == nil {
			o.PushFormula = publish.PushFormula
		}
		if err := brewPublish(o, cfg, res); err != nil {
			msg := fmt.Sprintf("homebrew channel skipped: %v", err)
			if o.Strict {
				return res, fmt.Errorf("%s", msg)
			}
			res.Warnings = append(res.Warnings, msg)
		} else {
			_, _ = fmt.Fprintf(log, "homebrew formula pushed to %s\n", o.Brew.Tap)
		}
	}

	_, _ = fmt.Fprintf(log, "released %s — %d asset(s), verified\n", res.Tag, len(res.Assets))

	// post_release runs last, after publish + verify. The release above is
	// already permanent and correct — a hook failure is reported as its own
	// error (exit non-zero) but can never un-release; recovery is simply
	// re-running the script, or `volt release --from-tag` for the pair.
	if cfg.Hooks.PostRelease != "" {
		if err := runHook("post_release", cfg.Hooks.PostRelease, o, res, cfg, log); err != nil {
			return res, fmt.Errorf("release %s SUCCEEDED, but the post_release hook failed: %w", res.Tag, err)
		}
	}
	return res, nil
}

// runHook executes one project-owned script with the release context in
// VOLT_* env vars, cwd = the repo root, output streamed to the log. The
// script is executed directly so any executable works; the permission-denied
// case gets the chmod hint because it is the #1 first-run mistake.
func runHook(name, script string, o Options, res Result, cfg voltcfg.Config, log io.Writer) error {
	_, _ = fmt.Fprintf(log, "hook %s: %s\n", name, script)
	cmd := exec.Command(script)
	cmd.Dir = o.Root
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(),
		"VOLT_HOOK="+name,
		"VOLT_TAG="+res.Tag,
		"VOLT_VERSION="+o.Version,
		"VOLT_DIR="+o.Dir,
		"VOLT_BINARY="+cfg.Binary,
		"VOLT_KIND="+string(res.Kind),
		"VOLT_DIST="+distDirOf(o),
	)
	if err := cmd.Run(); err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("%s is not executable — chmod +x it (or give it a shebang): %w", script, err)
		}
		return fmt.Errorf("%s: %w", script, err)
	}
	return nil
}

// distDirOf resolves the artifact directory the same way every consumer of
// Options does — one rule, referenced not repeated.
func distDirOf(o Options) string {
	if o.DistDir != "" {
		return o.DistDir
	}
	return gobuild.DistDirName
}

// collectArtifacts gathers pre-built archives for --from-artifacts and
// REFUSES an incomplete platform set — a partial release looks successful
// and cannot be corrected, because the tag is permanent.
func collectArtifacts(o Options, cfg voltcfg.Config) ([]string, error) {
	dist := o.DistDir
	if dist == "" {
		dist = gobuild.DistDirName
	}
	plats := platform.Default
	if len(cfg.Platforms) > 0 {
		var err error
		plats, err = platform.ParseList(cfg.Platforms)
		if err != nil {
			return nil, err
		}
	}
	var assets []string
	var missing []string
	for _, p := range plats {
		name := p.AssetName(cfg.Binary, o.Version)
		path := filepath.Join(dist, name)
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, p.String())
			continue
		}
		assets = append(assets, path)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("--from-artifacts: %s missing from %s for platform(s) %s — refusing a partial release (the tag is permanent); collect every runner's artifacts first",
			cfg.Binary, dist, strings.Join(missing, ", "))
	}
	// Checksums are recomputed HERE, across the collected set — each
	// runner's own checksums.txt covers only its platform.
	names := make([]string, len(assets))
	for i, a := range assets {
		names[i] = filepath.Base(a)
	}
	sums, err := archive.WriteChecksums(dist, names)
	if err != nil {
		return nil, err
	}
	return append(assets, sums), nil
}

// brewPublish renders and pushes the formula for a released CLI.
func brewPublish(o Options, cfg voltcfg.Config, res Result) error {
	dist := o.DistDir
	if dist == "" {
		dist = gobuild.DistDirName
	}
	sums, err := publish.SumsFromChecksums(filepath.Join(dist, archive.ChecksumsFileName), cfg.Binary)
	if err != nil {
		return err
	}
	if len(sums) == 0 {
		return fmt.Errorf("no tar.gz assets in checksums.txt — nothing for brew to install")
	}
	formula, err := publish.RenderFormula(publish.FormulaData{
		Binary: cfg.Binary, Desc: o.Brew.Description, Repo: o.Repo,
		Tag: res.Tag, BareVersion: strings.TrimPrefix(o.Version, "v"),
		License: o.Brew.License, Sums: sums,
	})
	if err != nil {
		return err
	}
	return o.PushFormula(o.Brew.Tap, cfg.Binary, formula, fmt.Sprintf("%s %s", cfg.Binary, o.Version))
}

// reserve pushes the tag — the atomic lock (ADR-R10). An explicit version
// never retries: if the push is rejected, someone owns that version and
// silently shipping the next number would be worse than stopping.
func reserve(root, tag string, log io.Writer) error {
	if err := gitx.CreateTag(root, tag); err != nil {
		return err
	}
	if err := gitx.PushTag(root, Remote, tag); err != nil {
		if _, rejected := err.(gitx.ErrTagRejected); rejected {
			return fmt.Errorf("%s already exists on %s — pick another version (a published version is permanent)", tag, Remote)
		}
		return err
	}
	_, _ = fmt.Fprintf(log, "reserved tag %s\n", tag)
	return nil
}

// buildOnly is the snapshot path: steps 1–3 and 6, no tag, no publish,
// dist/ kept.
func buildOnly(o Options, cfg voltcfg.Config, res Result, log io.Writer) (Result, error) {
	kind := res.Kind
	if kind == detect.KindLibrary {
		return res, fmt.Errorf("--snapshot builds binaries; %s is a library (nothing to build)", o.Dir)
	}
	b, err := gobuild.Run(gobuild.Options{Dir: filepath.Join(o.Root, o.Dir), Version: o.Version, DistDir: o.DistDir, Log: log}, cfg)
	if err != nil {
		return res, err
	}
	res.Tag = o.Version
	res.Warnings = b.Warnings
	res.Assets = b.Assets
	return res, nil
}

// runGo executes a go subcommand in dir, returning combined output.
func runGo(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// recomputeChecksums rewrites dist/checksums.txt to cover every asset in
// res.Assets, and ensures the file appears in the asset list exactly once.
// Called whenever an asset is added after the build wrote the original.
func recomputeChecksums(o Options, res *Result) error {
	dist := distDirOf(o)
	var names []string
	var kept []string
	for _, a := range res.Assets {
		if filepath.Base(a) == archive.ChecksumsFileName {
			continue // regenerated below; drop any prior entry
		}
		kept = append(kept, a)
		names = append(names, filepath.Base(a))
	}
	sums, err := archive.WriteChecksums(dist, names)
	if err != nil {
		return err
	}
	res.Assets = append(kept, sums)
	return nil
}

// buildSkillsBundle tars a managed skills directory into dist as
// skills_<version>.tar.gz. Resolution order for WHICH skills/ ships with a
// release: the released directory's own, else the repo root's — a monorepo
// CLI may carry its own skills while a single-CLI repo keeps them top-level.
// Returns "" when neither exists or the found one is unmanaged.
func buildSkillsBundle(o Options, cfg voltcfg.Config, absDir string) (string, error) {
	candidates := []string{
		filepath.Join(absDir, cfg.Skills.SkillsDir()),
		filepath.Join(o.Root, cfg.Skills.SkillsDir()),
	}
	var skillsDir string
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			skillsDir = c
			break
		}
	}
	if skillsDir == "" || cfg.Skills.ManagedDisabled() {
		return "", nil
	}
	dist := o.DistDir
	if dist == "" {
		dist = gobuild.DistDirName
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return "", err
	}
	// PAIRED CONSTANT: skillcmd's fetcher requests this exact asset name
	// (skillcmd/cache.go bundleAssetName). No shared constant is possible —
	// volt imports nothing from kit modules (ADR-R08) — so a change here
	// must change there too, or every consumer fetch 404s.
	dest := filepath.Join(dist, "skills_"+o.Version+".tar.gz")
	if err := archive.TarGzDir(dest, skillsDir, "skills"); err != nil {
		return "", err
	}
	return dest, nil
}
