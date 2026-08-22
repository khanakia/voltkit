package appdir

import (
	"fmt"
	"path/filepath"
)

// osNativeDir maps a category onto the platform's conventional directory.
//
// The full table, with <app> being the app key:
//
//	           linux                                  darwin                              windows
//	config     $XDG_CONFIG_HOME or ~/.config/<app>    ~/Library/Application Support/<app>  %AppData%\<app>
//	data       $XDG_DATA_HOME   or ~/.local/share     ~/Library/Application Support/<app>  %LocalAppData%\<app>
//	cache      $XDG_CACHE_HOME  or ~/.cache/<app>     ~/Library/Caches/<app>               %LocalAppData%\<app>\Cache
//	state      $XDG_STATE_HOME  or ~/.local/state     ~/Library/Logs/<app>                 %LocalAppData%\<app>\State
//
// Hand-rolled rather than taken from a dependency for one reason: with the
// mapping local, every platform's row is exercised in CI by injecting the
// platform and environment. A dependency's behaviour on a platform the project
// does not run would be taken on faith.
//
// Note that Go's os.UserConfigDir and os.UserCacheDir are deliberately not used
// — they read the host's real environment, which would make two thirds of this
// table untestable from any single developer machine.
func (c *config) osNativeDir(cat Category) (string, error) {
	switch c.platform {
	case Linux:
		return c.linuxDir(cat)
	case Darwin:
		return c.darwinDir(cat)
	case Windows:
		return c.windowsDir(cat)
	default:
		// Unreachable via New, which validates the platform.
		return "", fmt.Errorf("appdir: unknown platform %q", c.platform)
	}
}

// linuxDir follows the XDG Base Directory specification, honouring each
// variable when set and absolute.
func (c *config) linuxDir(cat Category) (string, error) {
	var envVar string
	var fallback []string

	switch cat {
	case Config:
		envVar, fallback = "XDG_CONFIG_HOME", []string{".config"}
	case Data:
		envVar, fallback = "XDG_DATA_HOME", []string{".local", "share"}
	case Cache:
		envVar, fallback = "XDG_CACHE_HOME", []string{".cache"}
	case State:
		envVar, fallback = "XDG_STATE_HOME", []string{".local", "state"}
	default:
		return "", fmt.Errorf("appdir: unknown category %q", cat)
	}

	// The spec requires XDG variables to be absolute; a relative value is
	// defined as invalid and must be ignored rather than resolved against cwd.
	if v, ok := c.lookupEnv(envVar); ok && filepath.IsAbs(v) {
		return filepath.Join(v, c.appKey), nil
	}

	home, err := c.homeDir()
	if err != nil {
		return "", fmt.Errorf("appdir: home directory for %s: %w", cat, err)
	}
	return filepath.Join(append(append([]string{home}, fallback...), c.appKey)...), nil
}

// darwinDir follows Apple's layout. Config and data share Application Support:
// macOS draws no distinction between them, and inventing one would put files
// where no other Mac application looks.
func (c *config) darwinDir(cat Category) (string, error) {
	home, err := c.homeDir()
	if err != nil {
		return "", fmt.Errorf("appdir: home directory for %s: %w", cat, err)
	}

	switch cat {
	case Config, Data:
		return filepath.Join(home, "Library", "Application Support", c.appKey), nil
	case Cache:
		return filepath.Join(home, "Library", "Caches", c.appKey), nil
	case State:
		return filepath.Join(home, "Library", "Logs", c.appKey), nil
	default:
		return "", fmt.Errorf("appdir: unknown category %q", cat)
	}
}

// windowsDir splits on the roaming boundary: config goes to %AppData%, which
// roams with the user across machines in a domain, while data, cache, and state
// go to %LocalAppData%, which does not. Roaming a database or a cache would copy
// it across the network on every login.
func (c *config) windowsDir(cat Category) (string, error) {
	// Each base is resolved only for the category that needs it. Computing both
	// up front made a cache lookup fail whenever %AppData% was unset, even
	// though the cache never uses the roaming base.
	switch cat {
	case Config:
		roaming, err := c.windowsBase("APPDATA", filepath.Join("AppData", "Roaming"), cat)
		if err != nil {
			return "", err
		}
		return filepath.Join(roaming, c.appKey), nil
	case Data, Cache, State:
		local, err := c.windowsBase("LOCALAPPDATA", filepath.Join("AppData", "Local"), cat)
		if err != nil {
			return "", err
		}
		switch cat {
		case Cache:
			return filepath.Join(local, c.appKey, "Cache"), nil
		case State:
			return filepath.Join(local, c.appKey, "State"), nil
		default:
			return filepath.Join(local, c.appKey), nil
		}
	default:
		return "", fmt.Errorf("appdir: unknown category %q", cat)
	}
}

// windowsBase reads a Windows base-directory variable, falling back to the
// conventional path under the home directory when it is unset — which happens in
// stripped service environments and in tests.
func (c *config) windowsBase(envVar, rel string, cat Category) (string, error) {
	if v, ok := c.lookupEnv(envVar); ok && v != "" {
		return v, nil
	}
	home, err := c.homeDir()
	if err != nil {
		return "", fmt.Errorf("appdir: %s unset and no home directory for %s: %w", envVar, cat, err)
	}
	return filepath.Join(home, rel), nil
}
