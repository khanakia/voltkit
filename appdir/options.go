package appdir

// Option customises resolution.
//
// Functional options rather than an exported config struct: new knobs can be
// added without touching a single existing call site, and the smallest valid
// call stays New("appkey") forever.
type Option func(*config)

// WithDirName overrides the project-local and home-dotfile directory name.
// Defaults to "." + app key.
func WithDirName(name string) Option {
	return func(c *config) { c.dirName = name }
}

// WithEnvPrefix overrides the environment variable prefix. Defaults to the
// upper-cased app key, so "volt" yields VOLT_HOME and VOLT_CACHE_DIR.
func WithEnvPrefix(prefix string) Option {
	return func(c *config) { c.prefix = prefix }
}

// WithLayout sets which strategy each category uses. Categories the layout omits
// fall back to DefaultLayout, so overriding one does not mean restating four.
func WithLayout(l Layout) Option {
	return func(c *config) { c.layout = l }
}

// WithCustom pins one category to an absolute path, overriding everything else
// including the environment.
//
// A layout naming CustomPath without a matching WithCustom is a construction
// error rather than a silent fallback.
func WithCustom(cat Category, path string) Option {
	return func(c *config) { c.custom[cat] = path }
}

// WithPlatform overrides OS detection.
//
// This is what makes the OS-native table testable: without it the Windows and
// Linux rows never execute on a macOS developer machine or a Linux CI runner,
// and two thirds of the mapping ships unverified.
func WithPlatform(p Platform) Option {
	return func(c *config) { c.platform = p }
}

// WithEnvLookup overrides the environment source.
//
// Pass it explicitly in tests even when expecting nothing — otherwise a stray
// VOLT_CACHE_DIR in a developer's shell silently changes the result.
func WithEnvLookup(lookup func(string) (string, bool)) Option {
	return func(c *config) { c.lookupEnv = lookup }
}

// WithHomeDir overrides home directory discovery.
func WithHomeDir(fn func() (string, error)) Option {
	return func(c *config) { c.homeDir = fn }
}

// WithWorkingDir fixes the directory the project-local search starts from.
func WithWorkingDir(dir string) Option {
	return func(c *config) {
		c.workDir = func() (string, error) { return dir, nil }
	}
}

// WithWorkingDirFunc overrides working directory discovery, including its error
// path, which WithWorkingDir cannot express.
func WithWorkingDirFunc(fn func() (string, error)) Option {
	return func(c *config) { c.workDir = fn }
}
