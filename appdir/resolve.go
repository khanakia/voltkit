package appdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// absPath is the seam for tests to drive the failure branches below.
// filepath.Abs only errors when the path is relative AND os.Getwd fails, which
// cannot be arranged from a test without mutating process state — so the
// fallbacks would otherwise ship unverified.
var absPath = filepath.Abs

// config is the resolved option set.
type config struct {
	appKey   string
	dirName  string
	prefix   string
	layout   Layout
	platform Platform
	custom   map[Category]string

	// Seams. Every external input is injected so tests never mutate process
	// state — a test that chdirs or sets an environment variable breaks its
	// neighbours when the package is run with -race or in parallel.
	lookupEnv func(string) (string, bool)
	homeDir   func() (string, error)
	workDir   func() (string, error)
}

// validate rejects configuration that cannot produce a sane path, at
// construction time rather than at the moment a file is written.
func (c *config) validate() error {
	if c.dirName == "" {
		return fmt.Errorf("appdir: dir name must not be empty")
	}
	if c.prefix == "" {
		return fmt.Errorf("appdir: env prefix must not be empty")
	}
	if !c.platform.Valid() {
		return fmt.Errorf("appdir: unknown platform %q", c.platform)
	}
	if err := c.layout.Validate(); err != nil {
		return err
	}
	for cat, path := range c.custom {
		if !cat.Valid() {
			return fmt.Errorf("appdir: custom path given for unknown category %q", cat)
		}
		if path == "" {
			return fmt.Errorf("appdir: custom path for %q must not be empty", cat)
		}
	}
	// A layout naming CustomPath without supplying one is a mistake, not a
	// request to fall back — falling back would silently ignore the caller's
	// explicit intent to place that category somewhere specific.
	for _, cat := range Categories() {
		if c.layout.resolve(cat) == CustomPath {
			if _, ok := c.custom[cat]; !ok {
				return fmt.Errorf("appdir: category %q uses the custom strategy but no path was supplied; use WithCustom", cat)
			}
		}
	}
	return nil
}

// resolveCategory walks the precedence chain for one category.
//
// Order, highest first: an explicit custom path, the per-category environment
// variable, the collapse-everything <PREFIX>_HOME variable, then the layout's
// strategy. First hit wins.
func (c *config) resolveCategory(cat Category) resolved {
	if path, ok := c.custom[cat]; ok {
		return resolved{dir: cleanAbs(path), rung: RungCustom}
	}

	// A set-but-empty variable is not an override. Treating it as one would
	// resolve the category to the current working directory.
	if v, ok := c.lookupEnv(c.envDirVar(cat)); ok && v != "" {
		return resolved{dir: cleanAbs(v), rung: RungEnvCategory}
	}

	if v, ok := c.lookupEnv(c.prefix + EnvHomeSuffix); ok && v != "" {
		return resolved{dir: filepath.Join(cleanAbs(v), string(cat)), rung: RungEnvHome}
	}

	return c.byStrategy(cat, c.layout.resolve(cat))
}

// envDirVar builds the per-category override variable name.
func (c *config) envDirVar(cat Category) string {
	return c.prefix + "_" + upper(string(cat)) + EnvDirSuffix
}

// byStrategy applies one strategy, handling the project-local fallback rules.
func (c *config) byStrategy(cat Category, s Strategy) resolved {
	switch s {
	case ProjectLocal:
		dir, err := c.findProjectDir()
		if err == nil {
			return resolved{dir: filepath.Join(dir, categorySubdir(cat)), rung: RungProjectLocal}
		}
		// Data is the one category that must NOT fall back. Resolving elsewhere
		// opens a second, empty database, and the user reads that as data loss.
		// Config and the disposable categories fall back happily — user-level
		// defaults applying outside a project is normal, exactly as git treats
		// ~/.gitconfig.
		if cat == Data {
			return resolved{err: err}
		}
		fb := c.byStrategy(cat, HomeDotfile)
		fb.fellBack = true
		return fb

	case HomeDotfile:
		home, err := c.homeDir()
		if err != nil {
			// No home directory is not fatal: the OS-native mapping can still
			// work from environment variables on Windows.
			fb := c.byStrategy(cat, OSNative)
			fb.fellBack = true
			return fb
		}
		return resolved{dir: filepath.Join(home, c.dirName, categorySubdir(cat)), rung: RungHomeDotfile}

	case OSNative:
		dir, err := c.osNativeDir(cat)
		if err != nil {
			return resolved{err: err}
		}
		return resolved{dir: dir, rung: RungOSNative}

	default:
		// CustomPath without a path is rejected in validate, and every other
		// value is rejected by Layout.Validate, so this is unreachable via New.
		return resolved{err: fmt.Errorf("appdir: unsupported strategy %q for category %q", s, cat)}
	}
}

// categorySubdir is the sub-path a category occupies when several categories
// share one root — project-local, home-dotfile, or <PREFIX>_HOME.
//
// Config sits at the root rather than in a subdirectory so that a project's
// config file lands at <DirName>/config.json: visible, and committable on its
// own while the sibling data/, cache/, and state/ directories are gitignored.
// Burying it one level deeper would force users to gitignore the whole tree or
// enumerate exceptions.
func categorySubdir(c Category) string {
	if c == Config {
		return ""
	}
	return string(c)
}

// findProjectDir walks up from the working directory looking for dirName.
//
// The loop terminates when filepath.Dir stops changing, which is what happens at
// "/" and at a Windows volume root. Comparing against a literal "/" would spin
// forever on Windows.
func (c *config) findProjectDir() (string, error) {
	start, err := c.workDir()
	if err != nil {
		return "", fmt.Errorf("appdir: working directory: %w", err)
	}
	dir, err := absPath(start)
	if err != nil {
		return "", fmt.Errorf("appdir: resolve %q: %w", start, err)
	}

	for {
		candidate := filepath.Join(dir, c.dirName)
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w: no %s directory at or above %s", ErrNoProjectDir, c.dirName, start)
		}
		dir = parent
	}
}

// cleanAbs normalises a caller- or environment-supplied path.
//
// Absolute because commands may chdir, and a relative path resolved once would
// then point somewhere else. Cleaned so a supplied path cannot walk out of an
// intended tree via "..".
func cleanAbs(path string) string {
	if abs, err := absPath(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// upper is an ASCII upper-caser. Category names are ASCII literals defined in
// this package, so strings.ToUpper's Unicode handling is not needed.
func upper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}
