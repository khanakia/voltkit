// Package detect answers the one question rule two of the spec turns on:
// is this directory a CLI (package main) or a library?
//
// Detection over configuration (ADR-R06): Go already knows the answer, so
// asking the user to declare it would only create a field that can drift.
package detect

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kind is what a directory releases.
type Kind string

const (
	// KindCLI — package main: binaries are built, archived and uploaded.
	KindCLI Kind = "cli"
	// KindLibrary — any other package: the release is a tag plus notes, and
	// the artifact that matters is the module proxy entry.
	KindLibrary Kind = "library"
)

// Dir reports what dir releases by asking `go list` for the package name.
//
// One shape has no package to ask about: a module root whose .go files all
// live in subdirectories — aws-sdk-go-v2's root module is exactly this
// (go.mod at the root, packages in aws/ and internal/). That is a library:
// the release is the tag, and consumers import its subpackages. Detected by
// go.mod-present-but-no-.go-files BEFORE go list, so a real go list failure
// (broken syntax) still errors loudly.
//
// A directory with neither a package nor a go.mod is a hard error — the
// right outcome per ADR-R06, because guessing would eventually publish a
// permanent wrong tag.
func Dir(dir string) (Kind, error) {
	if noGoFiles(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return KindLibrary, nil // module root without a root package — the aws-sdk-go-v2 shape
		}
		return "", fmt.Errorf("detect kind of %s: no Go files and no go.mod — nothing to release here", dir)
	}
	cmd := exec.Command("go", "list", "-f", "{{.Name}}", ".")
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("detect kind of %s: go list failed: %s", dir, strings.TrimSpace(errb.String()))
	}
	if strings.TrimSpace(out.String()) == "main" {
		return KindCLI, nil
	}
	return KindLibrary, nil
}

// noGoFiles reports whether dir contains no .go files directly (subdirs do
// not count — package membership is per-directory in Go).
func noGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return false
		}
	}
	return true
}
