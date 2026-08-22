// Package streams answers "what is releasable here, and where does each
// stream stand?" — the engine behind `volt status` and `volt release --bump`.
//
// A stream is one releasable directory's tag lineage: bare vX.Y.Z for the
// repo root, <binary>/vX.Y.Z for a CLI, <dir-path>/vX.Y.Z for a library
// (rule one of the spec). Discovery reads the repo — modules by go.mod walk,
// main packages by `go list` per module — so there is nothing to configure
// and nothing to drift.
package streams

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/cicheck"
	"github.com/khanakia/voltkit/apps/volt/detect"
	"github.com/khanakia/voltkit/apps/volt/gitx"
	"github.com/khanakia/voltkit/apps/volt/relname"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"
)

// Stream is one releasable directory's state.
type Stream struct {
	Dir          string      `json:"dir"`           // repo-relative; "." for the root
	Kind         detect.Kind `json:"kind"`          // cli | library
	Prefix       string      `json:"prefix"`        // tag prefix incl. trailing "/"; "" for bare
	LastVersion  string      `json:"last"`          // newest released version, "" when unreleased
	CommitsAhead int         `json:"commits_ahead"` // commits touching Dir since LastVersion's tag; 0 when unreleased or current
	Suggested    string      `json:"suggested"`     // next patch version when CommitsAhead > 0
}

// Discover enumerates every releasable directory under root with its stream
// state. Order: root first (if releasable), then lexicographic.
func Discover(root string) ([]Stream, error) {
	// Absolute root up front: main-package discovery maps go list's absolute
	// directories back to repo-relative ones with filepath.Rel, which FAILS
	// against a relative root — and the command always passes ".". Caught
	// live: `volt status` silently showed no CLI streams at all.
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dirs, err := releasableDirs(root)
	if err != nil {
		return nil, err
	}
	var out []Stream
	for _, dir := range dirs {
		if voltcfg.IsInternal(root, dir) {
			continue // marked never-released; see voltcfg.Config.Internal
		}
		st, err := describe(root, dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		out = append(out, st)
	}
	return out, nil
}

// releasableDirs lists module roots plus every main-package directory inside
// them — each is an independently taggable stream. One `go list` per module,
// never per directory.
func releasableDirs(root string) ([]string, error) {
	mods, err := cicheck.Modules(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, mod := range mods {
		add(mod)
		modAbs := filepath.Join(root, mod)
		out, err := goList(modAbs, "-f", "{{.Name}} {{.Dir}}", "./...")
		if err != nil {
			continue // a module that fails go list still shows as its own stream
		}
		for _, line := range strings.Split(out, "\n") {
			name, abs, ok := strings.Cut(line, " ")
			if !ok || name != "main" {
				continue
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				continue
			}
			add(filepath.ToSlash(rel))
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i] == "." {
			return true
		}
		if dirs[j] == "." {
			return false
		}
		return dirs[i] < dirs[j]
	})
	return dirs, nil
}

// describe resolves one directory's stream state.
func describe(root, dir string) (Stream, error) {
	st := Stream{Dir: dir}
	absDir := filepath.Join(root, dir)
	kind, err := detect.Dir(absDir)
	if err != nil {
		return st, err
	}
	st.Kind = kind

	cfg, err := voltcfg.Load(absDir)
	if err != nil {
		return st, err
	}
	st.Prefix, err = Prefix(kind, dir, cfg.Binary)
	if err != nil {
		return st, err
	}

	st.LastVersion = newestVersion(root, st.Prefix)
	if st.LastVersion == "" {
		return st, nil // unreleased — nothing to count against
	}

	tag := st.Prefix + st.LastVersion
	st.CommitsAhead = commitsTouching(root, tag, dir)
	if st.CommitsAhead > 0 {
		if next, err := Bump(st.LastVersion, "patch"); err == nil {
			st.Suggested = next
		}
	}
	return st, nil
}

// Prefix derives a stream's tag prefix (with trailing slash; "" for the
// root) from the same rules relname.Compose applies.
func Prefix(kind detect.Kind, dir, binary string) (string, error) {
	// Compose with a throwaway version, then strip it — one source of truth
	// for the tag grammar instead of a re-implementation here.
	tag, err := relname.Compose(kind, dir, binary, "v0.0.0")
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(tag, "v0.0.0"), nil
}

// versionRE matches the bare-semver tail of a tag in this stream.
var versionRE = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// newestVersion returns the newest released version in a stream, "" if none.
// Matching is exact-shape: prefix + vX.Y.Z, so `notes/v1.0.0` never counts
// toward the `notes-extra/` stream and pre-releases are ignored.
func newestVersion(root, prefix string) string {
	tags, err := gitx.TagsWithPrefix(root, prefix)
	if err != nil {
		return ""
	}
	for _, t := range tags { // already newest-first
		rest := strings.TrimPrefix(t, prefix)
		if versionRE.MatchString(rest) {
			return rest
		}
	}
	return ""
}

// commitsTouching counts commits since tag that touch dir ("." = whole repo).
func commitsTouching(root, tag, dir string) int {
	args := []string{"rev-list", "--count", tag + "..HEAD"}
	if dir != "." {
		args = append(args, "--", dir)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	return n
}

// Bump computes the next version from current at the given level.
func Bump(current, level string) (string, error) {
	m := versionRE.FindStringSubmatch(current)
	if m == nil {
		return "", fmt.Errorf("cannot bump %q: not a bare vX.Y.Z version", current)
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	switch level {
	case "patch":
		pat++
	case "minor":
		min, pat = min+1, 0
	case "major":
		maj, min, pat = maj+1, 0, 0
	default:
		return "", fmt.Errorf("bump level %q: want patch, minor or major", level)
	}
	return fmt.Sprintf("v%d.%d.%d", maj, min, pat), nil
}

// FirstVersion is what an unreleased stream starts at when bumped.
const FirstVersion = "v0.1.0"

// Next resolves the version a --bump release of dir should use: the stream's
// newest version bumped, or FirstVersion for an unreleased stream.
func Next(root, dir, level string) (string, error) {
	absDir := filepath.Join(root, dir)
	kind, err := detect.Dir(absDir)
	if err != nil {
		return "", err
	}
	cfg, err := voltcfg.Load(absDir)
	if err != nil {
		return "", err
	}
	prefix, err := Prefix(kind, filepath.ToSlash(filepath.Clean(dir)), cfg.Binary)
	if err != nil {
		return "", err
	}
	current := newestVersion(root, prefix)
	if current == "" {
		return FirstVersion, nil
	}
	return Bump(current, level)
}

func goList(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go list: %s", strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
