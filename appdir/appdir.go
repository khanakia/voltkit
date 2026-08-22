package appdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvHomeSuffix is appended to the prefix for the collapse-everything override,
// e.g. prefix "VOLT" yields VOLT_HOME.
const EnvHomeSuffix = "_HOME"

// EnvDirSuffix is appended to "<PREFIX>_<CATEGORY>" for a per-category override,
// e.g. VOLT_CACHE_DIR.
const EnvDirSuffix = "_DIR"

// DirPerm is the permission for directories this package creates.
//
// 0700 rather than 0755: the data category holds whatever the user chose to
// store, and on a shared machine a world-readable state directory leaks it.
// Restrictive by default is the only safe direction — a user who wants it looser
// can chmod, and Ensure leaves existing permissions alone.
const DirPerm os.FileMode = 0o700

// ErrNoProjectDir is returned when a project-local category finds no DirName
// directory between the working directory and the filesystem root.
var ErrNoProjectDir = errors.New("no project directory found")

// resolved is one category's outcome.
type resolved struct {
	dir  string
	rung Rung
	// fellBack records that the configured strategy did not apply and a
	// lower-priority one was used. Surfaced so diagnostics can say "project-local
	// not found, fell back to home-dotfile" rather than silently reporting the
	// fallback as if it were the intent.
	fellBack bool
	// err is deferred rather than returned from New: a command that only needs
	// the cache must still run outside a project, even though Data cannot
	// resolve there.
	err error
}

// Dirs is the resolved set of directories for one application.
type Dirs struct {
	appKey  string
	dirName string
	prefix  string
	byCat   map[Category]resolved
}

// New resolves every category for the given application key.
//
// appKey is the application's name, used for the OS-native directory and as the
// basis for the derived defaults: DirName becomes "."+appKey and EnvPrefix
// becomes the upper-cased key. Both are overridable.
//
// The returned error covers configuration problems — an empty key, an invalid
// layout, a Custom strategy with no path. Per-category resolution failures are
// deferred to the accessor for that category, so a command needing only the
// cache still works outside a project.
func New(appKey string, opts ...Option) (*Dirs, error) {
	if strings.TrimSpace(appKey) == "" {
		return nil, errors.New("appdir: app key must not be empty")
	}

	cfg := config{
		appKey:    appKey,
		dirName:   "." + appKey,
		prefix:    strings.ToUpper(appKey),
		layout:    DefaultLayout,
		platform:  CurrentPlatform(),
		lookupEnv: os.LookupEnv,
		homeDir:   os.UserHomeDir,
		workDir:   os.Getwd,
		custom:    map[Category]string{},
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	d := &Dirs{
		appKey:  cfg.appKey,
		dirName: cfg.dirName,
		prefix:  cfg.prefix,
		byCat:   make(map[Category]resolved, len(Categories())),
	}
	for _, c := range Categories() {
		d.byCat[c] = cfg.resolveCategory(c)
	}
	return d, nil
}

// AppKey returns the application key these directories were built for.
func (d *Dirs) AppKey() string { return d.appKey }

// EnvHomeVar returns the name of the collapse-everything variable, e.g. VOLT_HOME.
func (d *Dirs) EnvHomeVar() string { return d.prefix + EnvHomeSuffix }

// EnvDirVar returns the per-category override variable name, e.g. VOLT_CACHE_DIR.
func (d *Dirs) EnvDirVar(c Category) string {
	return d.prefix + "_" + strings.ToUpper(string(c)) + EnvDirSuffix
}

// Dir returns the directory for a category.
//
// Accessors return an error rather than panicking or yielding an empty string
// because the common failure — a project-local data directory outside any
// project — is a normal user situation, not a programming mistake.
func (d *Dirs) Dir(c Category) (string, error) {
	r, ok := d.byCat[c]
	if !ok {
		return "", fmt.Errorf("appdir: unknown category %q", c)
	}
	if r.err != nil {
		return "", r.err
	}
	return r.dir, nil
}

// Rung reports which rule produced the category's directory.
func (d *Dirs) Rung(c Category) (Rung, error) {
	r, ok := d.byCat[c]
	if !ok {
		return "", fmt.Errorf("appdir: unknown category %q", c)
	}
	if r.err != nil {
		return "", r.err
	}
	return r.rung, nil
}

// FellBack reports whether the configured strategy did not apply and a
// lower-priority one produced the directory.
func (d *Dirs) FellBack(c Category) (bool, error) {
	r, ok := d.byCat[c]
	if !ok {
		return false, fmt.Errorf("appdir: unknown category %q", c)
	}
	if r.err != nil {
		return false, r.err
	}
	return r.fellBack, nil
}

// Path joins elements onto a category's directory.
//
// Pure path arithmetic — nothing is created. Whether a missing directory is an
// error (most commands) or something to create (init) is the caller's decision.
func (d *Dirs) Path(c Category, elem ...string) (string, error) {
	dir, err := d.Dir(c)
	if err != nil {
		return "", err
	}
	if len(elem) == 0 {
		return dir, nil
	}
	return filepath.Join(append([]string{dir}, elem...)...), nil
}

// ConfigFile returns a path inside the config category.
func (d *Dirs) ConfigFile(elem ...string) (string, error) { return d.Path(Config, elem...) }

// DataFile returns a path inside the data category.
//
// This is what persistence asks for. The database filename is the caller's
// knowledge — this package deliberately does not know what a database is.
func (d *Dirs) DataFile(elem ...string) (string, error) { return d.Path(Data, elem...) }

// CacheDir returns a path inside the cache category, nesting as deep as given.
func (d *Dirs) CacheDir(elem ...string) (string, error) { return d.Path(Cache, elem...) }

// StateDir returns a path inside the state category.
func (d *Dirs) StateDir(elem ...string) (string, error) { return d.Path(State, elem...) }

// Ensure creates a category's directory if absent.
//
// Existing directories keep their permissions: a user who deliberately widened
// one should not have that silently reverted on the next command.
func (d *Dirs) Ensure(c Category) error {
	dir, err := d.Dir(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("appdir: create %s directory %s: %w", c, dir, err)
	}
	return nil
}

// EnsureAll creates every resolvable category directory.
//
// Categories that failed to resolve are skipped rather than aborting the whole
// call: outside a project, EnsureAll should still create the cache and state
// directories that resolved perfectly well.
func (d *Dirs) EnsureAll() error {
	for _, c := range Categories() {
		if d.byCat[c].err != nil {
			continue
		}
		if err := d.Ensure(c); err != nil {
			return err
		}
	}
	return nil
}

// VerifyNotSymlink refuses a path that is a symlink.
//
// Anyone able to write into a state directory could otherwise replace a file
// with a link to a target elsewhere, and the next write would clobber that
// target. os.Lstat is required — os.Stat follows the link and reports the
// victim, so it structurally cannot detect this.
//
// A missing path is not an error: there is nothing there to impersonate.
func VerifyNotSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("appdir: inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("appdir: refusing to use %s: it is a symlink", path)
	}
	return nil
}
