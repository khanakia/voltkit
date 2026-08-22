// Package gobuild cross-compiles one directory for a set of platforms and
// packages the results — the core of `volt build` (spec, "Step by step").
//
// No network, no tokens, no git writes: this package must stay safe to run
// anywhere, because it is also what the tests exercise end-to-end.
package gobuild

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/archive"
	"github.com/khanakia/voltkit/apps/volt/buildmeta"
	"github.com/khanakia/voltkit/apps/volt/platform"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"
)

// DistDirName is where artifacts land, relative to the working directory —
// the same contract goreleaser users already know.
const DistDirName = "dist"

// Options is one build invocation.
type Options struct {
	// Dir is the directory holding package main, e.g. "./cmd/notes".
	Dir string
	// Version is stamped into the binary and into asset names.
	Version string
	// NativeOnly restricts the matrix to the host platform (the cgo path).
	NativeOnly bool
	// DistDir overrides the artifact directory; empty → ./dist.
	DistDir string
	// Log receives human progress lines; nil → io.Discard. An explicit sink
	// rather than fmt.Println so tests can assert on what was reported —
	// skipped platforms MUST be reported (spec: no silent narrowing).
	Log io.Writer
}

// Result reports what one build produced.
type Result struct {
	Assets    []string // archive basenames, in build order
	Checksums string   // path to checksums.txt
	Warnings  []string // non-fatal findings, e.g. an ldflags symbol that did not land
}

// Run builds cfg's platform matrix for opts.Dir. Artifacts land in DistDir as
// <binary>_<version>_<os>_<arch>.<ext> plus checksums.txt.
func Run(opts Options, cfg voltcfg.Config) (Result, error) {
	var res Result
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	if opts.Version == "" {
		return res, fmt.Errorf("a version is required — it is stamped into the binary and the asset names")
	}

	if err := Preflight(cfg, opts); err != nil {
		return res, err
	}
	plats, err := resolvePlatforms(cfg, opts, log)
	if err != nil {
		return res, err
	}

	vars := buildmeta.FromGit(opts.Dir, opts.Version)
	vars.Dir = opts.Dir
	vars.Binary = cfg.Binary

	distDir := opts.DistDir
	if distDir == "" {
		distDir = DistDirName
	}
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return res, err
	}
	// Scratch space for raw binaries; the archives are the deliverable.
	scratch, err := os.MkdirTemp("", "volt-build-*")
	if err != nil {
		return res, err
	}
	defer func() { _ = os.RemoveAll(scratch) }() // scratch cleanup is best-effort

	for _, p := range plats {
		asset, warns, err := buildOne(opts, cfg, vars, p, scratch, distDir)
		if err != nil {
			return res, fmt.Errorf("%s: %w", p, err)
		}
		res.Warnings = append(res.Warnings, warns...)
		res.Assets = append(res.Assets, asset)
		_, _ = fmt.Fprintf(log, "built %s\n", asset)
	}

	res.Checksums, err = archive.WriteChecksums(distDir, res.Assets)
	if err != nil {
		return res, fmt.Errorf("checksums: %w", err)
	}
	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(log, "WARN: %s\n", w)
	}
	return res, nil
}

// resolvePlatforms applies config, then the --native-only narrowing — loudly:
// every skipped platform is named, because a silent skip would look like a
// complete release missing three platforms.
func resolvePlatforms(cfg voltcfg.Config, opts Options, log io.Writer) ([]platform.Platform, error) {
	plats := platform.Default
	if len(cfg.Platforms) > 0 {
		var err error
		plats, err = platform.ParseList(cfg.Platforms)
		if err != nil {
			return nil, err
		}
	}
	if !opts.NativeOnly {
		return plats, nil
	}
	host := platform.Platform{OS: hostOS(), Arch: hostArch()}
	var kept []platform.Platform
	for _, p := range plats {
		if p == host {
			kept = append(kept, p)
		} else {
			_, _ = fmt.Fprintf(log, "skipping %s (--native-only, host is %s)\n", p, host)
		}
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("--native-only: host %s is not in the platform set %v", host, plats)
	}
	return kept, nil
}

// buildOne compiles and archives a single platform.
func buildOne(opts Options, cfg voltcfg.Config, vars buildmeta.Vars, p platform.Platform, scratch, distDir string) (string, []string, error) {
	// Per-platform template vars.
	vars.OS, vars.Arch = p.OS, p.Arch
	if zt, ok := p.ZigTarget(); ok {
		vars.ZigTarget = zt
	} else {
		vars.ZigTarget = ""
	}

	ldflags, stamps, err := renderLDFlags(cfg.LDFlags, vars)
	if err != nil {
		return "", nil, err
	}

	binName := cfg.Binary + p.ExeSuffix()
	outPath := filepath.Join(scratch, p.OS+"_"+p.Arch, binName)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", nil, err
	}

	env := append(os.Environ(),
		"CGO_ENABLED="+cgoBit(cfg.CGO),
		"GOOS="+p.OS,
		"GOARCH="+p.Arch,
	)
	if cfg.CGO {
		// cgo cross-compilation needs a C compiler per target; rendered from
		// the toolchain templates. Pre-flight (missing toolchain, darwin
		// target) is validated before we ever get here — see Preflight.
		if cc, err := vars.Render(cfg.Toolchain.CC); err == nil && cc != "" {
			env = append(env, "CC="+cc)
		}
		if cxx, err := vars.Render(cfg.Toolchain.CXX); err == nil && cxx != "" {
			env = append(env, "CXX="+cxx)
		}
	}

	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", outPath, ".")
	cmd.Dir = opts.Dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("go build: %s", strings.TrimSpace(string(out)))
	}

	// Verify the stamp landed, not just that the build was green: Go
	// silently ignores -X for a symbol that does not exist, which yields a
	// version-less binary from a green build (ADR-R08).
	warns := verifyStamps(outPath, stamps)

	entries := []archive.Entry{{Name: binName, Path: outPath, Mode: 0o755}}
	for _, extra := range cfg.ExtraFiles {
		entries = append(entries, archive.Entry{
			Name: filepath.Base(extra),
			Path: filepath.Join(opts.Dir, extra),
			Mode: 0o644,
		})
	}

	asset := p.AssetName(cfg.Binary, opts.Version)
	dest := filepath.Join(distDir, asset)
	if p.ArchiveExt() == ".zip" {
		err = archive.WriteZip(dest, entries)
	} else {
		err = archive.WriteTarGz(dest, entries)
	}
	if err != nil {
		return "", nil, err
	}
	return asset, warns, nil
}

// stamp is one -X assignment, kept for post-build verification.
type stamp struct {
	Symbol string
	Value  string
}

// renderLDFlags assembles the linker flags from config + vars and returns the
// flag string plus every -X stamp it applied (for verification). Symbols are
// emitted in sorted order so the command line — and therefore the binary — is
// reproducible across runs.
func renderLDFlags(lf voltcfg.LDFlags, vars buildmeta.Vars) (string, []stamp, error) {
	var flags []string
	if lf.Strip != nil && *lf.Strip {
		flags = append(flags, "-s", "-w")
	}
	// -buildid= strips the toolchain's random build id for reproducibility.
	flags = append(flags, "-buildid=")

	symbols := make([]string, 0, len(lf.Vars))
	for sym := range lf.Vars {
		symbols = append(symbols, sym)
	}
	sortStrings(symbols)
	stamps := make([]stamp, 0, len(symbols))
	for _, sym := range symbols {
		val, err := vars.Render(lf.Vars[sym])
		if err != nil {
			return "", nil, fmt.Errorf("ldflags var %s: %w", sym, err)
		}
		flags = append(flags, "-X", fmt.Sprintf("%s=%s", sym, val))
		stamps = append(stamps, stamp{Symbol: sym, Value: val})
	}
	flags = append(flags, lf.Extra...)
	return strings.Join(flags, " "), stamps, nil
}

// verifyStamps checks each -X stamp actually landed in the produced binary.
// Failures are warnings, not errors: an unstamped build still works — but the
// silence is what must not happen (ADR-R08).
//
// Why not `go tool nm`: -s (the strip default) removes the symbol table, so
// nm finds nothing on exactly the binaries volt ships. The stamped VALUE,
// however, survives in the binary's string data regardless of stripping — so
// we search for the value bytes. Honest limits: an empty rendered value is
// unverifiable (reported as such), and a value coinciding with unrelated
// bytes makes the check lenient, never noisy.
func verifyStamps(binPath string, stamps []stamp) []string {
	if len(stamps) == 0 {
		return nil
	}
	bin, err := os.ReadFile(binPath)
	if err != nil {
		return []string{fmt.Sprintf("could not verify ldflags stamps: read %s: %v", filepath.Base(binPath), err)}
	}
	var warns []string
	for _, st := range stamps {
		if st.Value == "" {
			warns = append(warns, fmt.Sprintf("-X %s rendered to an empty string — nothing was stamped and nothing can be verified", st.Symbol))
			continue
		}
		if !bytes.Contains(bin, []byte(st.Value)) {
			warns = append(warns, fmt.Sprintf("-X target %q not found in %s (value %q absent) — likely a nonexistent symbol; fix ldflags.vars in %s", st.Symbol, filepath.Base(binPath), st.Value, voltcfg.FileName))
		}
	}
	return warns
}

func cgoBit(on bool) string {
	if on {
		return "1"
	}
	return "0"
}
