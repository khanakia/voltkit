package appdir

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestOSNative_Table walks the entire platform × category matrix from a single
// host. Without the injected platform seam two thirds of these rows would never
// execute until a user on that OS hit them.
func TestOSNative_Table(t *testing.T) {
	const home = "/home/u"
	tests := []struct {
		name     string
		platform Platform
		env      map[string]string
		want     map[Category]string
	}{
		{
			name:     "linux without XDG variables",
			platform: Linux,
			env:      nil,
			want: map[Category]string{
				Config: filepath.Join(home, ".config", "volt"),
				Data:   filepath.Join(home, ".local", "share", "volt"),
				Cache:  filepath.Join(home, ".cache", "volt"),
				State:  filepath.Join(home, ".local", "state", "volt"),
			},
		},
		{
			name:     "linux honours every XDG variable",
			platform: Linux,
			env: map[string]string{
				"XDG_CONFIG_HOME": "/xdg/config",
				"XDG_DATA_HOME":   "/xdg/data",
				"XDG_CACHE_HOME":  "/xdg/cache",
				"XDG_STATE_HOME":  "/xdg/state",
			},
			want: map[Category]string{
				Config: filepath.Join("/xdg/config", "volt"),
				Data:   filepath.Join("/xdg/data", "volt"),
				Cache:  filepath.Join("/xdg/cache", "volt"),
				State:  filepath.Join("/xdg/state", "volt"),
			},
		},
		{
			// The XDG spec defines a relative value as invalid; honouring it
			// would resolve state against the working directory.
			name:     "linux ignores a relative XDG value",
			platform: Linux,
			env:      map[string]string{"XDG_CACHE_HOME": "relative/cache"},
			want:     map[Category]string{Cache: filepath.Join(home, ".cache", "volt")},
		},
		{
			name:     "darwin",
			platform: Darwin,
			env:      map[string]string{"XDG_CACHE_HOME": "/xdg/cache"}, // must be ignored
			want: map[Category]string{
				Config: filepath.Join(home, "Library", "Application Support", "volt"),
				Data:   filepath.Join(home, "Library", "Application Support", "volt"),
				Cache:  filepath.Join(home, "Library", "Caches", "volt"),
				State:  filepath.Join(home, "Library", "Logs", "volt"),
			},
		},
		{
			name:     "windows with both base variables set",
			platform: Windows,
			env: map[string]string{
				"APPDATA":      `C:\Users\u\AppData\Roaming`,
				"LOCALAPPDATA": `C:\Users\u\AppData\Local`,
			},
			want: map[Category]string{
				Config: filepath.Join(`C:\Users\u\AppData\Roaming`, "volt"),
				Data:   filepath.Join(`C:\Users\u\AppData\Local`, "volt"),
				Cache:  filepath.Join(`C:\Users\u\AppData\Local`, "volt", "Cache"),
				State:  filepath.Join(`C:\Users\u\AppData\Local`, "volt", "State"),
			},
		},
		{
			// Stripped service environments and containers routinely lack these.
			name:     "windows falls back to the conventional path when unset",
			platform: Windows,
			env:      nil,
			want: map[Category]string{
				Config: filepath.Join(home, "AppData", "Roaming", "volt"),
				Data:   filepath.Join(home, "AppData", "Local", "volt"),
				Cache:  filepath.Join(home, "AppData", "Local", "volt", "Cache"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := New("volt",
				WithPlatform(tt.platform),
				WithLayout(AllOSNative),
				WithEnvLookup(envMap(tt.env)),
				homeAt(home),
				WithWorkingDir("/tmp/nowhere"),
			)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for cat, want := range tt.want {
				got, err := d.Dir(cat)
				if err != nil {
					t.Fatalf("Dir(%s): %v", cat, err)
				}
				if got != want {
					t.Errorf("Dir(%s) = %q, want %q", cat, got, want)
				}
				if rung, _ := d.Rung(cat); rung != RungOSNative {
					t.Errorf("Rung(%s) = %q, want %q", cat, rung, RungOSNative)
				}
			}
		})
	}
}

// TestOSNative_SeparatesCacheFromData is the reason categories exist at all: a
// cache sharing a directory with the database gets backed up and synced forever.
func TestOSNative_SeparatesCacheFromData(t *testing.T) {
	for _, p := range []Platform{Linux, Darwin, Windows} {
		t.Run(string(p), func(t *testing.T) {
			d, err := New("volt",
				WithPlatform(p), WithLayout(AllOSNative),
				WithEnvLookup(envMap(map[string]string{
					"APPDATA": `C:\R`, "LOCALAPPDATA": `C:\L`,
				})),
				homeAt("/home/u"), WithWorkingDir("/tmp/nowhere"),
			)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			data, _ := d.Dir(Data)
			cache, _ := d.Dir(Cache)
			if data == cache {
				t.Errorf("%s: data and cache share %q — the cache would be backed up with the database", p, data)
			}
		})
	}
}

// TestOSNative_NoHomeIsReported covers a stripped environment on the platforms
// that cannot fall back to variables.
func TestOSNative_NoHomeIsReported(t *testing.T) {
	for _, p := range []Platform{Linux, Darwin, Windows} {
		t.Run(string(p), func(t *testing.T) {
			d, err := New("volt",
				WithPlatform(p), WithLayout(AllOSNative),
				WithEnvLookup(noEnv), brokenHome(), WithWorkingDir("/tmp/nowhere"),
			)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = d.Dir(Data)
			if err == nil {
				t.Fatalf("%s: Dir(Data) = nil error with no home directory", p)
			}
			if !strings.Contains(err.Error(), "home directory") {
				t.Errorf("%s: error should name the cause: %v", p, err)
			}
		})
	}
}

// TestOSNative_UnknownCategoryPerPlatform reaches the defensive branch in each
// platform mapping, which New's validation makes unreachable in normal use.
func TestOSNative_UnknownCategoryPerPlatform(t *testing.T) {
	c := &config{
		appKey:    "volt",
		lookupEnv: envMap(map[string]string{"APPDATA": `C:\R`, "LOCALAPPDATA": `C:\L`}),
		homeDir:   func() (string, error) { return "/home/u", nil },
	}

	for _, p := range []Platform{Linux, Darwin, Windows} {
		c.platform = p
		if _, err := c.osNativeDir("logs"); err == nil {
			t.Errorf("%s: osNativeDir(unknown) = nil error", p)
		}
	}

	c.platform = "plan9"
	if _, err := c.osNativeDir(Cache); err == nil {
		t.Error("osNativeDir with an unknown platform = nil error")
	}
}

// TestByStrategy_UnsupportedStrategy reaches the defensive default branch that
// New's validation makes unreachable through the public API.
func TestByStrategy_UnsupportedStrategy(t *testing.T) {
	c := &config{appKey: "volt", dirName: ".volt", prefix: "VOLT", platform: Linux, lookupEnv: noEnv}

	if got := c.byStrategy(Cache, "elsewhere"); got.err == nil {
		t.Error("byStrategy with an unknown strategy = nil error")
	}
}
