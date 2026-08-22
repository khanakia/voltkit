// Package version provides the reusable `version` command.
//
// It is the reference for how every command in this template is packaged: the
// library owns the BEHAVIOUR, the application owns the IDENTITY. Nothing here
// knows what the binary is called — the name is interpolated into help text at
// construction time. That is what lets one implementation serve every project
// scaffolded from this template, and lets those projects pick up fixes with a
// module bump instead of a re-copy.
//
// DEPENDENCY SHAPE: this package deliberately takes a plain binary name rather
// than the template's appmeta.Meta. It reads exactly one field, so requiring the
// whole struct would force anyone who wants just this command to adopt this
// template's config type as well. A library should ask for the narrowest thing
// it actually uses; a caller that does have a Meta simply passes meta.Binary.
//
// Wiring it up is one call:
//
//	root.AddCommand(version.New(
//	    version.WithBinaryName("myapp"),
//	    version.WithComponents(Component{Name: "db_schema", Version: 1}),
//	))
//
// The name is optional: with no option set it falls back to this process's own
// executable name, so version.New() alone produces a working command.
//
// Every user-visible string is overridable. Defaults are exported as functions
// (DefaultShort, DefaultLong, DefaultExample) so an app can wrap or extend them
// rather than having to restate the whole text to change one line.
package versioncmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/khanakia/voltkit/output"

	"github.com/spf13/cobra"
	"github.com/ubgo/buildinfo"
)

// jsonFlag names the --json flag once. WHY a constant: cmd.Flags().Changed on
// a misspelled name returns false instead of failing, so a typo here is a bug
// neither the compiler nor a test will point at. (Inlined from the retired
// voltkit/flags module when it was deleted, 2026-08-22.)
const jsonFlag = "json"

// Component is one contract surface an app versions independently of its binary
// version, e.g. {"db_schema", 1}.
//
// Integer rather than semver on purpose: these are negotiation counters that
// only ever increment, and an integer cannot be mis-compared the way "1.10"
// against "1.9" can be under a string sort.
type Component struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// Info is the payload rendered by this command.
//
// It is a PROJECTION of buildinfo.Info, not an embedding. Embedding would inline
// buildinfo's Modules field — the full dependency list — into every
// `version --json` invocation, which is noise for a question about the binary
// itself. Anything beyond this projection is available from buildinfo directly.
//
// Field names are the stable JSON contract: adding one is safe, renaming or
// removing one breaks every script and agent parsing this output.
type Info struct {
	// Binary is the executable name. Supplied by the app — buildinfo has no
	// business knowing what its consumer is called.
	Binary string `json:"binary"`

	// Version is the resolved version string.
	Version string `json:"version"`

	// Source reports which input produced Version: ldflags, module, or
	// unknown. See buildinfo.Source.
	Source buildinfo.Source `json:"source"`

	// Commit is the VCS revision, or buildinfo.Unknown.
	Commit string `json:"commit"`

	// BuildTime is the RFC3339 build timestamp, or buildinfo.Unknown.
	BuildTime string `json:"build_time"`

	// Modified reports an uncommitted tree at build time. Meaningful only
	// alongside a real commit — see HasCommit.
	Modified bool `json:"modified"`

	// GoVersion is the toolchain that produced the binary.
	GoVersion string `json:"go_version"`

	// Platform is GOOS/GOARCH, pre-joined because every renderer wants it that
	// way and none want the halves.
	Platform string `json:"platform"`

	// Components are the app's own contract versions.
	Components []Component `json:"components,omitempty"`
}

// HasCommit reports whether Commit holds a real hash rather than a sentinel.
//
// Compares against buildinfo.Unknown rather than a local copy of "unknown":
// duplicating another package's sentinel is a coupling that breaks silently if
// that package ever changes it.
func (i Info) HasCommit() bool {
	return i.Commit != "" && i.Commit != buildinfo.Unknown
}

// Collect assembles the Info for this binary.
//
// binary is the name to report and components are the app's contract versions;
// both are app knowledge, so they arrive as parameters rather than being
// discovered here.
func Collect(binary string, components ...Component) Info {
	bi := buildinfo.Get()
	return Info{
		Binary:     binary,
		Version:    bi.Version,
		Source:     bi.Source,
		Commit:     bi.Commit,
		BuildTime:  bi.BuildTime,
		Modified:   bi.Modified,
		GoVersion:  bi.GoVersion,
		Platform:   fmt.Sprintf("%s/%s", bi.GOOS, bi.GOARCH),
		Components: components,
	}
}

const (
	// DefaultUse is the command name as it appears on the command line.
	DefaultUse = "version"

	// KindVersionShow is the envelope `kind` for this command's JSON output.
	// Exported because it is a wire contract: consumers dispatch on it, so it
	// must be referenceable rather than retyped as a literal.
	KindVersionShow = "version.show"
)

// config is the resolved option set. Unexported so fields can be added without
// breaking callers — the functional options are the public surface.
type config struct {
	binaryName string
	use        string
	aliases    []string
	short      string
	long       string
	example    string
	hidden     bool
	components []Component
}

// Option customises the command. Functional options rather than an exported
// config struct because new knobs can then be added without touching a single
// existing call site, and the zero-option call stays valid forever.
type Option func(*config)

// WithBinaryName sets the executable name used in the generated examples.
//
// Callers holding this template's appmeta.Meta pass meta.Binary. Omit it and the
// command falls back to the running executable's own name.
func WithBinaryName(name string) Option {
	return func(c *config) { c.binaryName = name }
}

// WithComponents declares the app's own contract versions (DB schema, config
// schema, ...). Only the app knows what it versions, so the library never
// invents these.
func WithComponents(components ...Component) Option {
	return func(c *config) { c.components = components }
}

// WithUse renames the command, e.g. "ver".
func WithUse(use string) Option {
	return func(c *config) { c.use = use }
}

// WithAliases adds alternative names, e.g. "v".
func WithAliases(aliases ...string) Option {
	return func(c *config) { c.aliases = aliases }
}

// WithShort replaces the one-line description shown in the parent's help list.
func WithShort(short string) Option {
	return func(c *config) { c.short = short }
}

// WithLong replaces the full description. Compose instead of replace with
// DefaultLong(), e.g. WithLong(DefaultLong() + "\n\n" + extra).
func WithLong(long string) Option {
	return func(c *config) { c.long = long }
}

// WithExample replaces the examples block. See DefaultExample.
func WithExample(example string) Option {
	return func(c *config) { c.example = example }
}

// WithHidden removes the command from help without removing the command, for
// apps that expose version only through a root --version flag.
func WithHidden() Option {
	return func(c *config) { c.hidden = true }
}

// DefaultShort is the default one-line description.
func DefaultShort() string {
	return "Print version, build provenance, and component contract versions"
}

// DefaultLong is the default full description.
//
// Deliberately free of any binary name: it explains what the fields mean, which
// is identical for every app built from this template.
func DefaultLong() string {
	return `Report what this binary is and how it determined that.

The "source" field says which input produced the version:

  ldflags   stamped at build time by the release pipeline
  module    resolved by ` + "`go install <pkg>@<version>`" + `
  unknown   a local ` + "`go build`" + ` — the version shown is a placeholder

Component versions are contract surfaces that move independently of the binary
version; a patch release can still bump a schema.`
}

// DefaultExample renders the examples for a named binary.
//
// This is the one block that must name the binary. An empty name falls back to
// the running executable, then to the command name, so a caller that supplies
// nothing still gets readable text rather than a line starting with a stray
// space.
func DefaultExample(binary string) string {
	bin := resolveBinaryName(binary)
	return fmt.Sprintf(`  # human-readable
  %[1]s version

  # stable JSON for scripts and agents
  %[1]s version --%[2]s

  # just the version string
  %[1]s version --%[2]s | jq -r .data.version

  # how the version was determined: ldflags | module | unknown
  %[1]s version --%[2]s | jq -r .data.source`, bin, jsonFlag)
}

// resolveBinaryName resolves the name to print in help text.
//
// Never returns "": an empty name produces help output that reads as a
// formatting bug. os.Args[0] is the honest fallback — it is literally the name
// the user invoked — though under `go run` it is the temporary build artifact,
// which is why an app that knows its own name should say so.
// osArgs is the seam for tests to drive the fallback branches below. A test
// cannot arrange an empty or degenerate os.Args without mutating process state,
// so those paths would otherwise ship unverified — the same pattern
// ubgo/buildinfo uses for readBuildInfo.
var osArgs = func() []string { return os.Args }

func resolveBinaryName(name string) string {
	if name != "" {
		return name
	}
	if args := osArgs(); len(args) > 0 && args[0] != "" {
		if base := filepath.Base(args[0]); base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return DefaultUse
}

// New builds the version command.
func New(opts ...Option) *cobra.Command {
	cfg := config{
		use:   DefaultUse,
		short: DefaultShort(),
		long:  DefaultLong(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Resolved after the options run so WithBinaryName can feed the examples,
	// while an explicit WithExample still wins outright.
	cfg.binaryName = resolveBinaryName(cfg.binaryName)
	if cfg.example == "" {
		cfg.example = DefaultExample(cfg.binaryName)
	}

	var jsonOut bool

	cmd := &cobra.Command{
		Use:     cfg.use,
		Aliases: cfg.aliases,
		Short:   cfg.short,
		Long:    cfg.long,
		Example: cfg.example,
		Hidden:  cfg.hidden,

		// Declared explicitly: without it cobra accepts anything, and a typo'd
		// subcommand silently prints the version instead of erroring.
		Args: cobra.NoArgs,

		RunE: func(cmd *cobra.Command, args []string) error {
			info := Collect(cfg.binaryName, cfg.components...)

			if jsonOut {
				return output.JSON(cmd.OutOrStdout(), KindVersionShow, info, 0)
			}
			return WriteText(cmd.OutOrStdout(), info)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, jsonFlag, false, "emit the stable JSON envelope")
	return cmd
}

// WriteText renders the human form of info to w.
//
// Exported so an app that overrides RunE, or renders version inside a larger
// `doctor`-style report, can reuse the exact formatting rather than reproduce it.
// Everything is written to w rather than stdout so output stays capturable and
// redirectable.
func WriteText(w io.Writer, info Info) error {
	if _, err := fmt.Fprintf(w, "%s %s\n", info.Binary, info.Version); err != nil {
		return err
	}

	// Fixed rows use a constant width; component names are app-defined, so their
	// column is measured instead of guessed.
	const labelWidth = 12

	for _, row := range [][2]string{
		{"source", string(info.Source)},
		{"commit", commitLabel(info)},
		{"built", info.BuildTime},
		{"go", info.GoVersion},
		{"platform", info.Platform},
	} {
		if _, err := fmt.Fprintf(w, "  %-*s %s\n", labelWidth, row[0]+":", row[1]); err != nil {
			return err
		}
	}

	if len(info.Components) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "  components:"); err != nil {
		return err
	}
	width := componentLabelWidth(info.Components)
	for _, c := range info.Components {
		if _, err := fmt.Fprintf(w, "    %-*s v%d\n", width, c.Name+":", c.Version); err != nil {
			return err
		}
	}
	return nil
}

// componentLabelWidth returns the column width that keeps every component
// version aligned: the longest name plus its colon. Measured rather than fixed,
// because a hardcoded width misaligns the moment an app adds a longer name.
func componentLabelWidth(components []Component) int {
	width := 0
	for _, c := range components {
		if n := len(c.Name) + 1; n > width {
			width = n
		}
	}
	return width
}

// commitLabel appends a dirty marker so a build made from an uncommitted tree is
// obvious — a bare hash implies a reproducible build that does not exist.
//
// The marker is suppressed when there is no commit to qualify: "unknown (dirty)"
// asserts a dirty checkout for a build carrying no VCS record at all.
func commitLabel(info Info) string {
	if info.Modified && info.HasCommit() {
		return strings.TrimSpace(info.Commit) + " (dirty)"
	}
	return info.Commit
}
