package gobuild

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/khanakia/voltkit/apps/volt/voltcfg"
)

// scaffoldCLI writes a minimal buildable package main with a stampable
// version symbol, returning its directory.
func scaffoldCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.test/notes\n\ngo 1.22\n",
		"main.go":   "package main\n\nvar version = \"dev\"\n\nfunc main() { println(version) }\n",
		"README.md": "hello\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hostCfg(dir string) voltcfg.Config {
	c := voltcfg.Config{
		Platforms: []string{runtime.GOOS + "/" + runtime.GOARCH},
		LDFlags:   voltcfg.LDFlags{Vars: map[string]string{"main.version": "{{.Version}}"}},
	}
	c.ApplyDefaults(dir)
	return c
}

// End-to-end: build one platform, expect the archive + checksums, no warnings.
func TestRunProducesAssetAndChecksums(t *testing.T) {
	dir := scaffoldCLI(t)
	dist := t.TempDir()
	cfg := hostCfg(dir)
	cfg.ExtraFiles = []string{"README.md"}

	res, err := Run(Options{Dir: dir, Version: "v0.1.0", DistDir: dist}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assets) != 1 {
		t.Fatalf("assets: %v", res.Assets)
	}
	if _, err := os.Stat(filepath.Join(dist, res.Assets[0])); err != nil {
		t.Fatalf("asset missing on disk: %v", err)
	}
	if _, err := os.Stat(res.Checksums); err != nil {
		t.Fatalf("checksums missing: %v", err)
	}
	// main.version exists in the scaffold → the stamp must verify cleanly.
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
}

// A -X target that does not exist must WARN (not fail, not stay silent) —
// Go silently ignores it, which is exactly the defect ADR-R08 closes.
func TestRunWarnsOnMissingStampSymbol(t *testing.T) {
	dir := scaffoldCLI(t)
	cfg := hostCfg(dir)
	cfg.LDFlags.Vars = map[string]string{"main.doesNotExist": "{{.Version}}"}

	res, err := Run(Options{Dir: dir, Version: "v0.1.0", DistDir: t.TempDir()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "main.doesNotExist") {
		t.Fatalf("want a warning naming the missing symbol, got %v", res.Warnings)
	}
}

// --native-only must NAME every skipped platform (no silent narrowing).
func TestNativeOnlyReportsSkips(t *testing.T) {
	dir := scaffoldCLI(t)
	cfg := hostCfg(dir)
	// Host platform plus one guaranteed-foreign platform.
	foreign := "linux/arm64"
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		foreign = "darwin/amd64"
	}
	cfg.Platforms = append(cfg.Platforms, foreign)

	var log bytes.Buffer
	res, err := Run(Options{Dir: dir, Version: "v0.1.0", DistDir: t.TempDir(), NativeOnly: true, Log: &log}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assets) != 1 {
		t.Fatalf("native-only must build exactly the host platform: %v", res.Assets)
	}
	if !strings.Contains(log.String(), "skipping "+foreign) {
		t.Fatalf("skipped platform not reported:\n%s", log.String())
	}
}

// --native-only with a platform set that excludes the host is an error, not
// an empty success.
func TestNativeOnlyHostAbsentErrors(t *testing.T) {
	dir := scaffoldCLI(t)
	cfg := hostCfg(dir)
	cfg.Platforms = []string{"plan9/386"} // never the host
	if _, err := Run(Options{Dir: dir, Version: "v0.1.0", DistDir: t.TempDir(), NativeOnly: true}, cfg); err == nil {
		t.Fatal("want error when host is not in the platform set")
	}
}

func TestRunRequiresVersion(t *testing.T) {
	dir := scaffoldCLI(t)
	if _, err := Run(Options{Dir: dir}, hostCfg(dir)); err == nil {
		t.Fatal("want error on empty version")
	}
}

// renderLDFlags is deterministic: same inputs, same flag string (symbol order
// sorted), so rebuilt binaries are byte-comparable.
func TestRenderLDFlagsDeterministic(t *testing.T) {
	lf := voltcfg.LDFlags{Vars: map[string]string{
		"b.sym": "2", "a.sym": "1", "c.sym": "3",
	}}
	strip := true
	lf.Strip = &strip
	v := vars(t)
	first, stamps, err := renderLDFlags(lf, v)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, _, _ := renderLDFlags(lf, v)
		if again != first {
			t.Fatalf("non-deterministic: %q vs %q", first, again)
		}
	}
	var syms []string
	for _, st := range stamps {
		syms = append(syms, st.Symbol)
	}
	if strings.Join(syms, ",") != "a.sym,b.sym,c.sym" {
		t.Fatalf("symbols not sorted: %v", syms)
	}
	if !strings.HasPrefix(first, "-s -w -buildid= ") {
		t.Fatalf("strip flags missing: %q", first)
	}
}
