// Package appdir decides where a CLI stores its files: which directory, chosen
// how, and reported back so the choice is never a mystery.
//
// "Why is my data missing" is the most common support question a local-first
// tool gets, and it is nearly always this package's answer differing from what
// the user assumed. So every resolution records the Rung that produced it, ready
// for a doctor command to print.
//
// # Two axes
//
// A path is the product of two independent choices: the Category of file
// (config, data, cache, state) and the Strategy for where that category lives
// (project-local, home dotfile, OS-native, custom). Any category may use any
// strategy — that product is the point, because the useful default is not one
// strategy applied to everything. A cache in particular is machine-scoped: ten
// projects sharing one cache beats ten copies of identical downloads, and the
// OS-native cache directory is excluded from backups and sync by convention.
//
// # Dependency shape
//
// This package takes an app key and plain options, never a config struct
// belonging to the caller. A framework module that demands its consumer's
// identity type inverts the dependency — apps holding such a type adapt at the
// call site instead.
//
// See docsi/APPDIR_SPEC.md for the full design and its rationale.
package appdir

import (
	"fmt"
	"runtime"
)

// Category is a kind of file, grouped by lifecycle rather than by feature.
//
// The split exists because these have genuinely different handling: config and
// data want backing up, cache and state are disposable and must NOT be backed
// up. Storing them together forces every backup tool to treat throwaway bytes as
// precious ones.
type Category string

const (
	// Config holds settings a human edits. Precious; may be committed.
	Config Category = "config"

	// Data holds the database and user content. Precious; never committed.
	Data Category = "data"

	// Cache holds downloads and derived artifacts. Safe to delete at any
	// moment, and must never be backed up.
	Cache Category = "cache"

	// State holds logs, history, and run records. Disposable, but useful in a
	// support bundle.
	State Category = "state"
)

// Categories returns every category in a stable order.
//
// Stable so that doctor output and EnsureAll behave identically across runs; a
// map iteration here would reorder diagnostics between invocations.
func Categories() []Category {
	return []Category{Config, Data, Cache, State}
}

// Valid reports whether c is a known category.
func (c Category) Valid() bool {
	switch c {
	case Config, Data, Cache, State:
		return true
	}
	return false
}

// String implements fmt.Stringer.
func (c Category) String() string { return string(c) }

// Strategy is where a category's directory is placed.
type Strategy string

const (
	// ProjectLocal walks up from the working directory looking for DirName,
	// the way git finds .git. Use when state belongs to a repository.
	ProjectLocal Strategy = "project-local"

	// HomeDotfile is ~/<DirName>. One predictable path on every OS, and the
	// convention developers already know from ~/.aws, ~/.docker, ~/.kube.
	HomeDotfile Strategy = "home-dotfile"

	// OSNative is the platform's conventional directory for that category, so
	// backup tools and cache cleaners behave correctly without configuration.
	OSNative Strategy = "os-native"

	// CustomPath is an absolute path supplied by the caller. Set it with
	// WithCustom; naming a category Custom in a Layout without a path is an
	// error rather than a silent fallback.
	CustomPath Strategy = "custom"
)

// Valid reports whether s is a known strategy.
func (s Strategy) Valid() bool {
	switch s {
	case ProjectLocal, HomeDotfile, OSNative, CustomPath:
		return true
	}
	return false
}

// String implements fmt.Stringer.
func (s Strategy) String() string { return string(s) }

// Platform selects which OS-native mapping applies.
//
// Exposed and injectable so the Windows and Linux rows are exercised in CI from
// a macOS developer machine. Without this seam two thirds of the mapping table
// would never execute until a user on that platform hit it.
type Platform string

const (
	Linux   Platform = "linux"
	Darwin  Platform = "darwin"
	Windows Platform = "windows"
)

// Valid reports whether p is a supported platform.
func (p Platform) Valid() bool {
	switch p {
	case Linux, Darwin, Windows:
		return true
	}
	return false
}

// String implements fmt.Stringer.
func (p Platform) String() string { return string(p) }

// CurrentPlatform maps runtime.GOOS onto a supported Platform.
//
// Unrecognised systems report Linux: every remaining GOOS Go supports (freebsd,
// openbsd, netbsd, dragonfly, solaris, illumos, aix) follows the XDG convention,
// so that is the correct default rather than an error.
func CurrentPlatform() Platform { return platformFor(runtime.GOOS) }

// platformFor is the mapping split out from CurrentPlatform so every branch is
// reachable in a test. Calling CurrentPlatform directly can only ever exercise
// the row matching the host running the suite.
func platformFor(goos string) Platform {
	switch goos {
	case "darwin":
		return Darwin
	case "windows":
		return Windows
	default:
		return Linux
	}
}

// Layout assigns a Strategy to each Category.
//
// A layout need not be exhaustive — DefaultLayout fills any category the caller
// leaves unset, so an app overriding one category does not have to restate the
// other three.
type Layout map[Category]Strategy

// DefaultLayout is the out-of-box placement.
//
// Config and data are project-local because they describe this project. Cache
// and state are OS-native because they are machine-scoped: a shared cache
// deduplicates across every project, gets cleared in one place, and lands in a
// directory the OS already excludes from backups and sync.
var DefaultLayout = Layout{
	Config: ProjectLocal,
	Data:   ProjectLocal,
	Cache:  OSNative,
	State:  OSNative,
}

// AllProjectLocal keeps every category inside the project directory, the way
// .git holds everything for git.
var AllProjectLocal = Layout{
	Config: ProjectLocal,
	Data:   ProjectLocal,
	Cache:  ProjectLocal,
	State:  ProjectLocal,
}

// AllHomeDotfile places every category under ~/<DirName>, for a machine-wide
// tool with no project concept.
var AllHomeDotfile = Layout{
	Config: HomeDotfile,
	Data:   HomeDotfile,
	Cache:  HomeDotfile,
	State:  HomeDotfile,
}

// AllOSNative follows platform conventions for every category.
var AllOSNative = Layout{
	Config: OSNative,
	Data:   OSNative,
	Cache:  OSNative,
	State:  OSNative,
}

// resolve returns the strategy for c, falling back to DefaultLayout when the
// layout does not mention it.
func (l Layout) resolve(c Category) Strategy {
	if s, ok := l[c]; ok {
		return s
	}
	return DefaultLayout[c]
}

// Validate reports the first unknown category or strategy in the layout.
//
// Called during construction so a typo fails at startup rather than resolving
// somewhere arbitrary at the moment a file is written.
func (l Layout) Validate() error {
	for _, c := range Categories() {
		s, ok := l[c]
		if !ok {
			continue
		}
		if !s.Valid() {
			return fmt.Errorf("appdir: category %q has unknown strategy %q", c, s)
		}
	}
	for c := range l {
		if !c.Valid() {
			return fmt.Errorf("appdir: unknown category %q in layout", c)
		}
	}
	return nil
}

// Rung records which rule produced a directory, so diagnostics can explain a
// surprising path instead of merely stating it.
type Rung string

const (
	// RungCustom: an explicit WithCustom path.
	RungCustom Rung = "custom"

	// RungEnvCategory: the per-category <PREFIX>_<CATEGORY>_DIR variable.
	RungEnvCategory Rung = "env-category"

	// RungEnvHome: <PREFIX>_HOME collapsed every category into one directory.
	RungEnvHome Rung = "env-home"

	// RungProjectLocal: a DirName directory found by walking up.
	RungProjectLocal Rung = "project-local"

	// RungHomeDotfile: ~/<DirName>.
	RungHomeDotfile Rung = "home-dotfile"

	// RungOSNative: the platform's conventional directory.
	RungOSNative Rung = "os-native"
)

// String implements fmt.Stringer.
func (r Rung) String() string { return string(r) }
