package appdir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- helpers ----------------------------------------------------------------

// noEnv is an environment with nothing set. Passed explicitly everywhere so a
// stray VOLT_* or XDG_* variable in a developer's shell cannot change a result.
func noEnv(string) (string, bool) { return "", false }

// envMap turns a literal into a lookup, so each test states exactly the
// environment it depends on.
func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func homeAt(dir string) Option {
	return WithHomeDir(func() (string, error) { return dir, nil })
}

func brokenHome() Option {
	return WithHomeDir(func() (string, error) { return "", errors.New("no home") })
}

// newDirs builds Dirs with every seam pinned, so tests describe only what they
// care about.
func newDirs(t *testing.T, opts ...Option) *Dirs {
	t.Helper()
	base := []Option{WithEnvLookup(noEnv), homeAt("/home/u"), WithWorkingDir("/tmp/nowhere"), WithPlatform(Linux)}
	d, err := New("volt", append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// projectAt creates a project directory and returns its parent and a deep
// working directory inside it.
func projectAt(t *testing.T, dirName string) (root, deep string) {
	t.Helper()
	root = t.TempDir()
	deep = filepath.Join(root, "src", "very", "deep")
	for _, d := range []string{filepath.Join(root, dirName), deep} {
		if err := os.MkdirAll(d, DirPerm); err != nil {
			t.Fatal(err)
		}
	}
	return root, deep
}

func mustDir(t *testing.T, d *Dirs, c Category) string {
	t.Helper()
	got, err := d.Dir(c)
	if err != nil {
		t.Fatalf("Dir(%s): %v", c, err)
	}
	return got
}

// --- enums ------------------------------------------------------------------

func TestCategory_ValidAndString(t *testing.T) {
	for _, c := range Categories() {
		if !c.Valid() {
			t.Errorf("Category(%q).Valid() = false", c)
		}
		if c.String() != string(c) {
			t.Errorf("Category(%q).String() = %q", c, c.String())
		}
	}
	for _, c := range []Category{"", "logs", "CONFIG"} {
		if c.Valid() {
			t.Errorf("Category(%q).Valid() = true, want false", c)
		}
	}
}

// TestCategories_StableOrder matters because doctor output and EnsureAll iterate
// it; a map-ordered list would reorder diagnostics between runs.
func TestCategories_StableOrder(t *testing.T) {
	want := []Category{Config, Data, Cache, State}
	for i := 0; i < 5; i++ {
		got := Categories()
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d", len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("Categories()[%d] = %q, want %q", j, got[j], want[j])
			}
		}
	}
}

func TestStrategy_ValidAndString(t *testing.T) {
	for _, s := range []Strategy{ProjectLocal, HomeDotfile, OSNative, CustomPath} {
		if !s.Valid() {
			t.Errorf("Strategy(%q).Valid() = false", s)
		}
		if s.String() != string(s) {
			t.Errorf("Strategy(%q).String() = %q", s, s.String())
		}
	}
	for _, s := range []Strategy{"", "global", "PROJECT-LOCAL"} {
		if s.Valid() {
			t.Errorf("Strategy(%q).Valid() = true, want false", s)
		}
	}
}

func TestPlatform_ValidAndString(t *testing.T) {
	for _, p := range []Platform{Linux, Darwin, Windows} {
		if !p.Valid() {
			t.Errorf("Platform(%q).Valid() = false", p)
		}
		if p.String() != string(p) {
			t.Errorf("Platform(%q).String() = %q", p, p.String())
		}
	}
	for _, p := range []Platform{"", "plan9", "DARWIN"} {
		if p.Valid() {
			t.Errorf("Platform(%q).Valid() = true, want false", p)
		}
	}
}

// TestCurrentPlatform pins the mapping against the host actually running the
// suite, and documents that unrecognised systems take the XDG path.
func TestCurrentPlatform(t *testing.T) {
	got := CurrentPlatform()
	if !got.Valid() {
		t.Fatalf("CurrentPlatform() = %q, not a valid platform", got)
	}
	want := Linux
	switch runtime.GOOS {
	case "darwin":
		want = Darwin
	case "windows":
		want = Windows
	}
	if got != want {
		t.Errorf("CurrentPlatform() = %q, want %q for GOOS=%q", got, want, runtime.GOOS)
	}
}

func TestRung_String(t *testing.T) {
	if RungOSNative.String() != "os-native" {
		t.Errorf("RungOSNative.String() = %q", RungOSNative.String())
	}
}

// --- layout -----------------------------------------------------------------

// TestLayout_PartialFallsBackToDefault is why a caller overriding one category
// does not have to restate the other three.
func TestLayout_PartialFallsBackToDefault(t *testing.T) {
	l := Layout{Cache: ProjectLocal}

	if got := l.resolve(Cache); got != ProjectLocal {
		t.Errorf("resolve(Cache) = %q, want %q", got, ProjectLocal)
	}
	if got := l.resolve(Data); got != DefaultLayout[Data] {
		t.Errorf("resolve(Data) = %q, want the default %q", got, DefaultLayout[Data])
	}
}

func TestLayout_Validate(t *testing.T) {
	tests := []struct {
		name    string
		layout  Layout
		wantErr string
	}{
		{"empty is valid", Layout{}, ""},
		{"presets are valid", DefaultLayout, ""},
		{"unknown strategy", Layout{Cache: "elsewhere"}, "unknown strategy"},
		{"unknown category", Layout{"logs": OSNative}, "unknown category"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.layout.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPresets_CoverEveryCategory(t *testing.T) {
	presets := map[string]Layout{
		"DefaultLayout":   DefaultLayout,
		"AllProjectLocal": AllProjectLocal,
		"AllHomeDotfile":  AllHomeDotfile,
		"AllOSNative":     AllOSNative,
	}
	for name, l := range presets {
		if err := l.Validate(); err != nil {
			t.Errorf("%s.Validate() = %v", name, err)
		}
		for _, c := range Categories() {
			if _, ok := l[c]; !ok {
				t.Errorf("%s is missing category %q", name, c)
			}
		}
	}
}

// TestDefaultLayout_SharesTheCache pins the central design decision: a cache is
// machine-scoped, so it must not sit inside a project alongside the database.
func TestDefaultLayout_SharesTheCache(t *testing.T) {
	if DefaultLayout[Cache] != OSNative {
		t.Errorf("DefaultLayout[Cache] = %q, want %q — a per-project cache duplicates downloads and gets backed up", DefaultLayout[Cache], OSNative)
	}
	if DefaultLayout[Data] != ProjectLocal {
		t.Errorf("DefaultLayout[Data] = %q, want %q", DefaultLayout[Data], ProjectLocal)
	}
}

// --- construction -----------------------------------------------------------

func TestNew_DerivesDefaults(t *testing.T) {
	d := newDirs(t)

	if d.AppKey() != "volt" {
		t.Errorf("AppKey() = %q", d.AppKey())
	}
	if got := d.EnvHomeVar(); got != "VOLT_HOME" {
		t.Errorf("EnvHomeVar() = %q, want VOLT_HOME", got)
	}
	if got := d.EnvDirVar(Cache); got != "VOLT_CACHE_DIR" {
		t.Errorf("EnvDirVar(Cache) = %q, want VOLT_CACHE_DIR", got)
	}
}

func TestNew_RejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		opts    []Option
		wantErr string
	}{
		{"empty key", "", nil, "app key"},
		{"whitespace key", "   ", nil, "app key"},
		{"empty dir name", "volt", []Option{WithDirName("")}, "dir name"},
		{"empty prefix", "volt", []Option{WithEnvPrefix("")}, "env prefix"},
		{"bad platform", "volt", []Option{WithPlatform("plan9")}, "unknown platform"},
		{"bad layout", "volt", []Option{WithLayout(Layout{Cache: "nope"})}, "unknown strategy"},
		{"custom for unknown category", "volt", []Option{WithCustom("logs", "/tmp/x")}, "unknown category"},
		{"custom with empty path", "volt", []Option{WithCustom(Cache, "")}, "must not be empty"},
		{"custom strategy without a path", "volt", []Option{WithLayout(Layout{Cache: CustomPath})}, "no path was supplied"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.key, append([]Option{WithEnvLookup(noEnv)}, tt.opts...)...)
			if err == nil {
				t.Fatalf("New() = nil error, want one containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestNew_MinimalCall is the promise that the smallest useful call is one
// argument. It touches the real host, so it asserts only shape.
func TestNew_MinimalCall(t *testing.T) {
	d, err := New("volt")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, err := d.Dir(Cache); err != nil || got == "" {
		t.Errorf("Dir(Cache) = %q, %v — want a path from the real environment", got, err)
	}
}
