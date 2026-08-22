package appdir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- precedence -------------------------------------------------------------

// TestPrecedence_CustomBeatsEverything pins the top of the chain. The project
// directory and both environment variables are present, so a lower rung winning
// would be visible rather than coincidental.
func TestPrecedence_CustomBeatsEverything(t *testing.T) {
	_, deep := projectAt(t, ".volt")
	d := newDirs(t,
		WithWorkingDir(deep),
		WithCustom(Data, "/mnt/fast/data"),
		WithEnvLookup(envMap(map[string]string{
			"VOLT_DATA_DIR": "/from/env",
			"VOLT_HOME":     "/from/home",
		})),
	)

	if got := mustDir(t, d, Data); got != filepath.Clean("/mnt/fast/data") {
		t.Errorf("Dir(Data) = %q, want the custom path", got)
	}
	rung, _ := d.Rung(Data)
	if rung != RungCustom {
		t.Errorf("Rung(Data) = %q, want %q", rung, RungCustom)
	}
}

func TestPrecedence_CategoryEnvBeatsHomeEnvAndLayout(t *testing.T) {
	_, deep := projectAt(t, ".volt")
	d := newDirs(t,
		WithWorkingDir(deep),
		WithEnvLookup(envMap(map[string]string{
			"VOLT_CACHE_DIR": "/from/cache-env",
			"VOLT_HOME":      "/from/home",
		})),
	)

	if got := mustDir(t, d, Cache); got != filepath.Clean("/from/cache-env") {
		t.Errorf("Dir(Cache) = %q, want the per-category override", got)
	}
	rung, _ := d.Rung(Cache)
	if rung != RungEnvCategory {
		t.Errorf("Rung(Cache) = %q, want %q", rung, RungEnvCategory)
	}
}

// TestPrecedence_HomeEnvCollapsesEveryCategory covers the blunt escape hatch:
// one variable relocates the whole tree, each category in its own subdirectory.
func TestPrecedence_HomeEnvCollapsesEveryCategory(t *testing.T) {
	_, deep := projectAt(t, ".volt")
	d := newDirs(t,
		WithWorkingDir(deep),
		WithEnvLookup(envMap(map[string]string{"VOLT_HOME": "/opt/volt"})),
	)

	for _, c := range Categories() {
		want := filepath.Join("/opt/volt", string(c))
		if got := mustDir(t, d, c); got != want {
			t.Errorf("Dir(%s) = %q, want %q", c, got, want)
		}
		if rung, _ := d.Rung(c); rung != RungEnvHome {
			t.Errorf("Rung(%s) = %q, want %q", c, rung, RungEnvHome)
		}
	}
}

// TestPrecedence_EmptyEnvIsNotAnOverride guards VOLT_CACHE_DIR= — set but blank.
// Honouring it would resolve the category to the current working directory.
func TestPrecedence_EmptyEnvIsNotAnOverride(t *testing.T) {
	root, deep := projectAt(t, ".volt")
	d := newDirs(t,
		WithWorkingDir(deep),
		WithLayout(AllProjectLocal),
		WithEnvLookup(envMap(map[string]string{"VOLT_CACHE_DIR": "", "VOLT_HOME": ""})),
	)

	want := filepath.Join(root, ".volt", "cache")
	if got := mustDir(t, d, Cache); got != want {
		t.Errorf("Dir(Cache) = %q, want %q", got, want)
	}
}

// TestPrecedence_PathsAreAbsolute matters because commands may chdir; a relative
// path resolved once would then point somewhere else.
func TestPrecedence_PathsAreAbsolute(t *testing.T) {
	d := newDirs(t, WithCustom(Cache, "relative/cache"))

	got := mustDir(t, d, Cache)
	if !filepath.IsAbs(got) {
		t.Errorf("Dir(Cache) = %q, want an absolute path", got)
	}
}

// --- strategies -------------------------------------------------------------

// TestProjectLocal_WalksUp is the git model: a user deep in a tree expects the
// repository's state, not a new directory beside them.
func TestProjectLocal_WalksUp(t *testing.T) {
	root, deep := projectAt(t, ".volt")
	d := newDirs(t, WithWorkingDir(deep), WithLayout(AllProjectLocal))

	tests := []struct {
		cat  Category
		want string
	}{
		// Config sits at the root of the project directory so it can be
		// committed on its own while the siblings are gitignored.
		{Config, filepath.Join(root, ".volt")},
		{Data, filepath.Join(root, ".volt", "data")},
		{Cache, filepath.Join(root, ".volt", "cache")},
		{State, filepath.Join(root, ".volt", "state")},
	}
	for _, tt := range tests {
		if got := mustDir(t, d, tt.cat); got != tt.want {
			t.Errorf("Dir(%s) = %q, want %q", tt.cat, got, tt.want)
		}
		if rung, _ := d.Rung(tt.cat); rung != RungProjectLocal {
			t.Errorf("Rung(%s) = %q, want %q", tt.cat, rung, RungProjectLocal)
		}
	}
}

func TestProjectLocal_HonoursCustomDirName(t *testing.T) {
	root, deep := projectAt(t, ".acme")
	d := newDirs(t, WithWorkingDir(deep), WithDirName(".acme"), WithLayout(AllProjectLocal))

	if got := mustDir(t, d, Data); got != filepath.Join(root, ".acme", "data") {
		t.Errorf("Dir(Data) = %q", got)
	}
}

func TestHomeDotfile(t *testing.T) {
	d := newDirs(t, homeAt("/home/u"), WithLayout(AllHomeDotfile))

	tests := []struct {
		cat  Category
		want string
	}{
		{Config, filepath.Join("/home/u", ".volt")},
		{Data, filepath.Join("/home/u", ".volt", "data")},
		{Cache, filepath.Join("/home/u", ".volt", "cache")},
	}
	for _, tt := range tests {
		if got := mustDir(t, d, tt.cat); got != tt.want {
			t.Errorf("Dir(%s) = %q, want %q", tt.cat, got, tt.want)
		}
		if rung, _ := d.Rung(tt.cat); rung != RungHomeDotfile {
			t.Errorf("Rung(%s) = %q", tt.cat, rung)
		}
	}
}

// TestHomeDotfile_FallsBackWhenNoHome covers a stripped environment: with no
// home directory the OS-native mapping can still work from variables.
func TestHomeDotfile_FallsBackWhenNoHome(t *testing.T) {
	d := newDirs(t,
		brokenHome(),
		WithPlatform(Windows),
		WithLayout(AllHomeDotfile),
		WithEnvLookup(envMap(map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`})),
	)

	if got := mustDir(t, d, Cache); !strings.Contains(got, "Cache") {
		t.Errorf("Dir(Cache) = %q, want the OS-native fallback", got)
	}
	if fellBack, _ := d.FellBack(Cache); !fellBack {
		t.Error("FellBack(Cache) = false, want true")
	}
}

func TestCustomStrategy_UsesSuppliedPath(t *testing.T) {
	d := newDirs(t,
		WithLayout(Layout{Cache: CustomPath}),
		WithCustom(Cache, "/mnt/cache"),
	)

	if got := mustDir(t, d, Cache); got != filepath.Clean("/mnt/cache") {
		t.Errorf("Dir(Cache) = %q", got)
	}
}

// --- fallback semantics (ADR-002) -------------------------------------------

// TestNoProject_DataFails is the deliberate refusal. Falling back would open a
// second, empty database, which the user reads as data loss.
func TestNoProject_DataFails(t *testing.T) {
	d := newDirs(t, WithWorkingDir(t.TempDir()), WithLayout(AllProjectLocal))

	_, err := d.Dir(Data)
	if !errors.Is(err, ErrNoProjectDir) {
		t.Fatalf("Dir(Data) error = %v, want ErrNoProjectDir", err)
	}
	if !strings.Contains(err.Error(), ".volt") {
		t.Errorf("error should name the directory it looked for: %v", err)
	}
	if _, err := d.Rung(Data); err == nil {
		t.Error("Rung(Data) = nil error, want the resolution failure")
	}
	if _, err := d.FellBack(Data); err == nil {
		t.Error("FellBack(Data) = nil error, want the resolution failure")
	}
	if _, err := d.Path(Data, "volt.db"); err == nil {
		t.Error("Path(Data) = nil error, want the resolution failure")
	}
}

// TestNoProject_ConfigFallsBack is the counterpart: user-level defaults applying
// outside a project is normal, exactly as git treats ~/.gitconfig.
func TestNoProject_ConfigFallsBack(t *testing.T) {
	d := newDirs(t, WithWorkingDir(t.TempDir()), homeAt("/home/u"), WithLayout(AllProjectLocal))

	for _, c := range []Category{Config, Cache, State} {
		got := mustDir(t, d, c)
		if !strings.HasPrefix(got, filepath.Join("/home/u", ".volt")) {
			t.Errorf("Dir(%s) = %q, want a home-dotfile fallback", c, got)
		}
		if rung, _ := d.Rung(c); rung != RungHomeDotfile {
			t.Errorf("Rung(%s) = %q, want %q", c, rung, RungHomeDotfile)
		}
		if fellBack, _ := d.FellBack(c); !fellBack {
			t.Errorf("FellBack(%s) = false — diagnostics must be able to say the strategy did not apply", c)
		}
	}
}

// TestFellBack_FalseOnPrimaryHit ensures the flag distinguishes a fallback from
// the configured strategy actually applying.
func TestFellBack_FalseOnPrimaryHit(t *testing.T) {
	_, deep := projectAt(t, ".volt")
	d := newDirs(t, WithWorkingDir(deep), WithLayout(AllProjectLocal))

	if fellBack, _ := d.FellBack(Data); fellBack {
		t.Error("FellBack(Data) = true on a direct project-local hit")
	}
}

// TestWorkingDirError covers an unreadable working directory, which happens when
// the process's cwd is deleted underneath it.
func TestWorkingDirError(t *testing.T) {
	d := newDirs(t,
		WithWorkingDirFunc(func() (string, error) { return "", errors.New("cwd gone") }),
		WithLayout(AllProjectLocal),
	)

	if _, err := d.Dir(Data); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Errorf("Dir(Data) = %v, want a working-directory error", err)
	}
}

// --- accessors --------------------------------------------------------------

func TestAccessors(t *testing.T) {
	root, deep := projectAt(t, ".volt")
	d := newDirs(t, WithWorkingDir(deep), WithLayout(AllProjectLocal))

	tests := []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{"ConfigFile", func() (string, error) { return d.ConfigFile("config.json") }, filepath.Join(root, ".volt", "config.json")},
		{"DataFile", func() (string, error) { return d.DataFile("volt.db") }, filepath.Join(root, ".volt", "data", "volt.db")},
		{"CacheDir nested", func() (string, error) { return d.CacheDir("http", "v2") }, filepath.Join(root, ".volt", "cache", "http", "v2")},
		{"StateDir", func() (string, error) { return d.StateDir("logs") }, filepath.Join(root, ".volt", "state", "logs")},
		{"Path with no elements", func() (string, error) { return d.Path(Cache) }, filepath.Join(root, ".volt", "cache")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.got()
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestAccessors_UnknownCategory(t *testing.T) {
	d := newDirs(t)
	unknown := Category("logs")

	if _, err := d.Dir(unknown); err == nil {
		t.Error("Dir(unknown) = nil error")
	}
	if _, err := d.Rung(unknown); err == nil {
		t.Error("Rung(unknown) = nil error")
	}
	if _, err := d.FellBack(unknown); err == nil {
		t.Error("FellBack(unknown) = nil error")
	}
	if _, err := d.Path(unknown, "x"); err == nil {
		t.Error("Path(unknown) = nil error")
	}
	if err := d.Ensure(unknown); err == nil {
		t.Error("Ensure(unknown) = nil error")
	}
}

// --- Ensure -----------------------------------------------------------------

func TestEnsure_CreatesRestrictedAndIsIdempotent(t *testing.T) {
	base := t.TempDir()
	d := newDirs(t, WithEnvLookup(envMap(map[string]string{"VOLT_HOME": filepath.Join(base, "nested")})))

	for i := range 2 { // twice: creation must not fail or change anything
		if err := d.Ensure(Data); err != nil {
			t.Fatalf("Ensure call %d: %v", i+1, err)
		}
	}

	dir := mustDir(t, d, Data)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("Ensure did not create a directory")
	}
	if runtime := info.Mode().Perm(); runtime != DirPerm {
		t.Errorf("perm = %o, want %o — state must not be world-readable", runtime, DirPerm)
	}
}

// TestEnsureAll_SkipsUnresolvableCategories is why EnsureAll does not abort:
// outside a project it should still create the cache and state directories that
// resolved perfectly well.
func TestEnsureAll_SkipsUnresolvableCategories(t *testing.T) {
	home := t.TempDir()
	d := newDirs(t, WithWorkingDir(t.TempDir()), homeAt(home), WithLayout(AllProjectLocal))

	if _, err := d.Dir(Data); err == nil {
		t.Fatal("precondition: Data should be unresolvable here")
	}
	if err := d.EnsureAll(); err != nil {
		t.Fatalf("EnsureAll: %v", err)
	}
	if _, err := os.Stat(mustDir(t, d, Cache)); err != nil {
		t.Errorf("EnsureAll skipped a resolvable category: %v", err)
	}
}

func TestEnsure_ReportsCreationFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDirs(t, WithCustom(Cache, filepath.Join(file, "cache")))

	if err := d.Ensure(Cache); err == nil {
		t.Error("Ensure = nil error, want a failure creating under a regular file")
	}
	if err := d.EnsureAll(); err == nil {
		t.Error("EnsureAll = nil error, want the same failure surfaced")
	}
}

// --- symlink guard ----------------------------------------------------------

func TestVerifyNotSymlink(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing path is fine", func(t *testing.T) {
		if err := VerifyNotSymlink(filepath.Join(dir, "absent.db")); err != nil {
			t.Errorf("VerifyNotSymlink(absent) = %v, want nil", err)
		}
	})

	t.Run("regular file is fine", func(t *testing.T) {
		real := filepath.Join(dir, "real.db")
		if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := VerifyNotSymlink(real); err != nil {
			t.Errorf("VerifyNotSymlink(regular) = %v, want nil", err)
		}
	})

	t.Run("symlink is refused", func(t *testing.T) {
		victim := filepath.Join(dir, "victim")
		if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.db")
		if err := os.Symlink(victim, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		err := VerifyNotSymlink(link)
		if err == nil {
			t.Fatal("expected a symlinked path to be refused")
		}
		if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("error should say why: %v", err)
		}
	})

	t.Run("unreadable parent surfaces the error", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, DirPerm) })

		if err := VerifyNotSymlink(filepath.Join(locked, "x.db")); err == nil {
			t.Error("VerifyNotSymlink = nil, want the stat failure surfaced")
		}
	})
}

// --- helpers ----------------------------------------------------------------

func TestUpper(t *testing.T) {
	for in, want := range map[string]string{"cache": "CACHE", "": "", "Data": "DATA", "st4te": "ST4TE"} {
		if got := upper(in); got != want {
			t.Errorf("upper(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCategorySubdir(t *testing.T) {
	if got := categorySubdir(Config); got != "" {
		t.Errorf("categorySubdir(Config) = %q, want \"\" so config sits at the project root", got)
	}
	for _, c := range []Category{Data, Cache, State} {
		if got := categorySubdir(c); got != string(c) {
			t.Errorf("categorySubdir(%s) = %q", c, got)
		}
	}
}

// --- defensive branches -----------------------------------------------------

// withBrokenAbs drives the filepath.Abs failure path, which cannot be arranged
// through the public API without mutating process state.
func withBrokenAbs(t *testing.T) {
	t.Helper()
	prev := absPath
	absPath = func(string) (string, error) { return "", errors.New("abs failed") }
	t.Cleanup(func() { absPath = prev })
}

func TestFindProjectDir_ReportsAbsFailure(t *testing.T) {
	withBrokenAbs(t)
	c := &config{dirName: ".volt", workDir: func() (string, error) { return "some/path", nil }}

	if _, err := c.findProjectDir(); err == nil || !strings.Contains(err.Error(), "resolve") {
		t.Errorf("findProjectDir() = %v, want a resolve error", err)
	}
}

// TestCleanAbs_FallsBackToClean covers the degraded path: an un-absolutisable
// path is still cleaned, so ".." cannot escape into an unintended tree.
func TestCleanAbs_FallsBackToClean(t *testing.T) {
	withBrokenAbs(t)

	if got := cleanAbs("/tmp/a/../b"); got != filepath.Clean("/tmp/a/../b") {
		t.Errorf("cleanAbs() = %q, want the cleaned path", got)
	}
}

func TestPlatformFor(t *testing.T) {
	tests := map[string]Platform{
		"darwin":  Darwin,
		"windows": Windows,
		"linux":   Linux,
		// Every other GOOS Go supports follows the XDG convention, so Linux is
		// the correct default rather than an error.
		"freebsd": Linux,
		"illumos": Linux,
		"":        Linux,
	}
	for goos, want := range tests {
		if got := platformFor(goos); got != want {
			t.Errorf("platformFor(%q) = %q, want %q", goos, got, want)
		}
	}
}
