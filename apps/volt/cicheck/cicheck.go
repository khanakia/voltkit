// Package cicheck implements `volt ci` — the same gate locally and in CI
// (spec, "volt ci — the same gate locally and in CI").
//
// Per module: gofmt -l, go vet, go test -race, plus golangci-lint when it is
// installed. Module discovery walks for go.mod files, so a single-module
// repo, a go.work monorepo and a nested-modules repo all work unconfigured.
package cicheck

import (
	_ "embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/gitx"
)

//go:embed golangci.yml
var embeddedGolangci []byte

// skipDirs are never walked for modules — build outputs and vendored code.
var skipDirs = map[string]bool{".git": true, "dist": true, "bin": true, "node_modules": true, "vendor": true, "testdata": true}

// Modules returns every directory under root holding a go.mod, repo-relative,
// sorted. The repo root itself is included when it is a module.
func Modules(root string) ([]string, error) {
	var mods []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			mods = append(mods, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(mods)
	return mods, err
}

// Changed narrows modules to those touched by files changed vs base
// ("origin/main" typically). The mapping is prefix-match: a file belongs to
// the DEEPEST module whose directory prefixes it, so editing dbent/x.go runs
// dbent, not the root module too. A change matching no module (root docs,
// workflow files) returns every module — the safe "run everything" fallback
// the spec mandates over dependency-graph cleverness.
func Changed(root, base string) ([]string, error) {
	mods, err := Modules(root)
	if err != nil {
		return nil, err
	}
	if base == "" {
		return mods, nil // no base to diff against → run everything
	}
	files, err := gitx.ChangedFiles(root, base)
	if err != nil || len(files) == 0 {
		return mods, nil
	}
	hit := map[string]bool{}
	fallback := false
	for _, f := range files {
		if m := owningModule(mods, filepath.ToSlash(f)); m != "" {
			hit[m] = true
		} else {
			fallback = true
		}
	}
	if fallback || len(hit) == 0 {
		return mods, nil
	}
	var out []string
	for _, m := range mods {
		if hit[m] {
			out = append(out, m)
		}
	}
	return out, nil
}

// owningModule returns the deepest module whose path prefixes file, "" when
// none does.
func owningModule(mods []string, file string) string {
	best := ""
	for _, m := range mods {
		prefix := m + "/"
		if m == "." {
			// The root module owns a file only when no deeper module does —
			// handled by depth comparison below (len(".")==1 loses to any).
			prefix = ""
		}
		if strings.HasPrefix(file, prefix) && len(m) > len(best) {
			best = m
		}
	}
	if best == "" {
		for _, m := range mods {
			if m == "." {
				return "."
			}
		}
	}
	return best
}

// Gate runs the checks for one module directory. Failures collect rather
// than abort, so one run reports every problem — the difference between one
// fix-push and four.
//
// fix applies what tools can repair themselves (gofmt -w, golangci-lint
// --fix) BEFORE checking, so the run reports only what remains for a human.
func Gate(root, mod string, log io.Writer, fix bool) []string {
	dir := filepath.Join(root, mod)
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	_, _ = fmt.Fprintf(log, "── %s\n", mod)

	if fix {
		if out, err := runIn(dir, "gofmt", "-w", "."); err != nil {
			fail("%s: gofmt -w: %v\n%s", mod, err, out)
		}
	}

	// gofmt -l: list unformatted files, excluding generated trees by
	// convention (a gen/ dir is tool output, not hand-maintained code).
	if out, err := runIn(dir, "gofmt", "-l", "."); err != nil {
		fail("%s: gofmt: %v", mod, err)
	} else if files := strings.TrimSpace(out); files != "" {
		fail("%s: gofmt needed on:\n%s", mod, files)
	}

	if out, err := runIn(dir, "go", "vet", "./..."); err != nil {
		fail("%s: go vet:\n%s", mod, strings.TrimSpace(out))
	}

	// golangci-lint is optional-but-loud: absence is a notice, not silence —
	// a repo author must know the lint half of the gate did not run.
	// Config precedence: the repo's own .golangci.* wins (it may carve out
	// generated code); otherwise volt's EMBEDDED config applies, which is
	// how a new fleet-wide lint rule ships by releasing volt.
	if _, err := exec.LookPath("golangci-lint"); err == nil {
		args := []string{"run"}
		if fix {
			args = append(args, "--fix")
		}
		if !hasOwnLintConfig(dir) {
			cfgPath, cleanup, err := materializeEmbeddedConfig()
			if err != nil {
				fail("%s: embedded lint config: %v", mod, err)
			} else {
				defer cleanup()
				args = append(args, "-c", cfgPath)
			}
		}
		if out, err := runIn(dir, "golangci-lint", args...); err != nil {
			fail("%s: golangci-lint:\n%s", mod, strings.TrimSpace(out))
		}
	} else {
		_, _ = fmt.Fprintf(log, "   notice: golangci-lint not installed — lint checks skipped\n")
	}

	if out, err := runIn(dir, "go", "test", "-race", "./..."); err != nil {
		fail("%s: go test -race:\n%s", mod, strings.TrimSpace(out))
	}
	return problems
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// lintConfigNames are the filenames golangci-lint discovers on its own.
var lintConfigNames = []string{".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"}

// hasOwnLintConfig reports whether dir (or the repo above it) carries its own
// golangci config. Only dir and its parents matter — that mirrors
// golangci-lint's own discovery.
func hasOwnLintConfig(dir string) bool {
	for d := dir; ; d = filepath.Dir(d) {
		for _, n := range lintConfigNames {
			if _, err := os.Stat(filepath.Join(d, n)); err == nil {
				return true
			}
		}
		if parent := filepath.Dir(d); parent == d {
			return false
		}
	}
}

// materializeEmbeddedConfig writes the embedded config to a temp file for
// golangci-lint's -c flag (it does not read config from stdin).
func materializeEmbeddedConfig() (string, func(), error) {
	f, err := os.CreateTemp("", "volt-golangci-*.yml")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(embeddedGolangci); err != nil {
		_ = f.Close()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		return "", nil, err
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}
