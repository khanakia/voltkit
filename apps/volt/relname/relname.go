// Package relname implements rule one of the spec: the two tag shapes and
// the manifest-free resolution between tags and directories.
//
//	library → directory path      version/v0.3.0   (the module proxy resolves no other shape)
//	CLI     → binary name         notes/v1.4.0     (nothing resolves CLI tags; pick the readable one)
//	root    → bare version        v1.4.0
package relname

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/detect"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"
)

// versionRE matches the semver-ish tail volt accepts: vMAJOR.MINOR.PATCH with
// an optional pre-release/build suffix.
var versionRE = regexp.MustCompile(`^v\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]+)?$`)

// ValidVersion reports whether v is an acceptable version string.
func ValidVersion(v string) bool { return versionRE.MatchString(v) }

// Compose builds the tag for releasing dir at version, given its detected
// kind and (for CLIs) the binary name from config.
//
// dir is repo-relative ("." for the root). The root always yields the bare
// version regardless of kind — a single-thing repo needs no prefix.
func Compose(kind detect.Kind, dir, binary, version string) (string, error) {
	if !ValidVersion(version) {
		return "", fmt.Errorf("version %q: want vMAJOR.MINOR.PATCH (e.g. v1.4.0)", version)
	}
	clean := filepath.ToSlash(filepath.Clean(dir))
	if clean == "." || clean == "" {
		return version, nil
	}
	if kind == detect.KindLibrary {
		// The proxy-mandated shape: full directory path.
		return clean + "/" + version, nil
	}
	// CLI: the binary name, wherever the folder lives.
	if binary == "" {
		binary = filepath.Base(clean)
	}
	return binary + "/" + version, nil
}

// Resolved is the outcome of mapping a tag back to a directory.
type Resolved struct {
	Dir     string      // repo-relative directory to release
	Version string      // the vX.Y.Z tail
	Kind    detect.Kind // what the directory releases
}

// Resolve maps a pushed tag back to its directory — the CI path
// (`volt release --from-tag`). Four steps, hard errors for ambiguity and
// no-match (spec, "Resolving a tag back to a directory"): the version a
// wrong guess would publish is permanent.
func Resolve(root, tag string) (Resolved, error) {
	name, version, err := split(tag)
	if err != nil {
		return Resolved{}, err
	}
	if name == "" { // bare v1.4.0 → repo root
		kind, err := detect.Dir(root)
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{Dir: ".", Version: version, Kind: kind}, nil
	}

	// Step 1 — exact path (libraries; also a CLI whose binary name equals
	// its path). Exact-path first means a library can never be captured by
	// the name scan below.
	if st, err := os.Stat(filepath.Join(root, name)); err == nil && st.IsDir() {
		kind, err := detect.Dir(filepath.Join(root, name))
		if err != nil {
			return Resolved{}, fmt.Errorf("tag %s names directory %s, but: %w", tag, name, err)
		}
		return Resolved{Dir: name, Version: version, Kind: kind}, nil
	}

	// Step 2 — scan for main packages whose binary name matches.
	matches, err := scanForBinary(root, name)
	if err != nil {
		return Resolved{}, err
	}
	switch len(matches) {
	case 1:
		return Resolved{Dir: matches[0], Version: version, Kind: detect.KindCLI}, nil
	case 0:
		// Step 4 — no match: hard error, never a best-effort build.
		return Resolved{}, fmt.Errorf("tag %s: no directory at %q and no main package builds a binary named %q", tag, name, name)
	default:
		// Step 3 — ambiguity: hard error with the named fix.
		return Resolved{}, fmt.Errorf("tag %s: %d directories build a binary named %q (%s) — set `binary:` in .volt.yml to disambiguate", tag, len(matches), name, strings.Join(matches, ", "))
	}
}

// split separates "notes/v1.4.0" into ("notes", "v1.4.0"); bare "v1.4.0"
// yields ("", "v1.4.0").
func split(tag string) (name, version string, err error) {
	i := strings.LastIndex(tag, "/")
	if i < 0 {
		if !ValidVersion(tag) {
			return "", "", fmt.Errorf("tag %q: no vX.Y.Z version tail", tag)
		}
		return "", tag, nil
	}
	name, version = tag[:i], tag[i+1:]
	if !ValidVersion(version) {
		return "", "", fmt.Errorf("tag %q: %q is not a vX.Y.Z version", tag, version)
	}
	return name, version, nil
}

// skipDirs are never scanned: build outputs and VCS internals can contain
// stale main packages that must not capture a tag.
var skipDirs = map[string]bool{".git": true, "dist": true, "bin": true, "node_modules": true, "vendor": true}

// scanForBinary walks the repo for directories whose main package would
// produce a binary named name — the directory base name, or an explicit
// `binary:` in that directory's .volt.yml.
func scanForBinary(root, name string) ([]string, error) {
	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if skipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && path != root) {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		// Cheap pre-filter before shelling out to `go list`: the candidate
		// must claim the name via its base name or its .volt.yml binary.
		claims := filepath.Base(rel) == name
		if b := binaryOverride(path); b != "" {
			claims = b == name
		}
		if !claims || rel == "." {
			return nil
		}
		if voltcfg.IsInternal(root, rel) {
			return nil // never released → can never claim a tag
		}
		if kind, err := detect.Dir(path); err == nil && kind == detect.KindCLI {
			matches = append(matches, rel)
		}
		return nil
	})
	return matches, err
}

// binaryOverride reads just the `binary:` field of a .volt.yml, tolerating
// absence. A full config load is overkill mid-walk.
func binaryOverride(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ".volt.yml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "binary:"); ok {
			return strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"'`))
		}
	}
	return ""
}
