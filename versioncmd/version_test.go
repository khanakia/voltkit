package versioncmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/buildinfo"
)

// otherApp is deliberately nothing like this template's own identity, so any
// leaked hardcoded name shows up as a failure rather than passing by luck.
const otherApp = "acme"

// templateIdentity is what this template calls itself. The library must never
// contain it; the guard test asserts that against a command built for otherApp.
const templateIdentity = "volt"

// TestNew_HelpTextCarriesNoTemplateIdentity is the guard for this package's
// stated invariant. A library command must be able to SAY the app's name without
// KNOWING it: the name arrives via Meta and is interpolated at construction.
//
// Without this test a stray "volt" in an example survives indefinitely — it
// still reads correctly in this repo, and only misleads users of a renamed
// project, who have no reason to suspect the library.
func TestNew_HelpTextCarriesNoTemplateIdentity(t *testing.T) {
	cmd := New(WithBinaryName(otherApp))
	surfaces := map[string]string{
		"Use":     cmd.Use,
		"Short":   cmd.Short,
		"Long":    cmd.Long,
		"Example": cmd.Example,
	}

	for name, text := range surfaces {
		for _, leaked := range []string{templateIdentity, "." + templateIdentity} {
			if strings.Contains(text, leaked) {
				t.Errorf("%s leaks this template's identity %q:\n%s", name, leaked, text)
			}
		}
	}

	if !strings.Contains(cmd.Example, otherApp) {
		t.Errorf("Example does not name the app it was built for (%q):\n%s", otherApp, cmd.Example)
	}
}

// TestDefaultExample_FallsBackWhenMetaIncomplete covers a partially-filled Meta:
// an empty name renders help that looks like a formatting bug rather than text.
func TestDefaultExample_FallsBackWhenMetaIncomplete(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		want   string
	}{
		{"explicit name is used verbatim", "ln", "ln"},
		{"empty name falls back to the executable", "", filepath.Base(os.Args[0])},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultExample(tt.binary)
			if !strings.Contains(got, tt.want) {
				t.Errorf("DefaultExample() does not use %q:\n%s", tt.want, got)
			}
			if !strings.Contains(got, tt.want+" "+DefaultUse) {
				t.Errorf("example does not invoke %q:\n%s", tt.want+" "+DefaultUse, got)
			}
		})
	}
}

func TestOptions_OverrideEveryUserVisibleString(t *testing.T) {
	cmd := New(
		WithBinaryName(otherApp),
		WithUse("ver"),
		WithAliases("v", "V"),
		WithShort("custom short"),
		WithLong("custom long"),
		WithExample("custom example"),
		WithHidden(),
	)

	if cmd.Use != "ver" {
		t.Errorf("Use = %q, want %q", cmd.Use, "ver")
	}
	if len(cmd.Aliases) != 2 {
		t.Errorf("Aliases = %v, want 2 entries", cmd.Aliases)
	}
	if cmd.Short != "custom short" {
		t.Errorf("Short = %q", cmd.Short)
	}
	if cmd.Long != "custom long" {
		t.Errorf("Long = %q", cmd.Long)
	}
	if cmd.Example != "custom example" {
		t.Errorf("Example = %q", cmd.Example)
	}
	if !cmd.Hidden {
		t.Error("WithHidden did not hide the command")
	}
}

// TestDefaults_AreComposable pins the reason the defaults are exported: an app
// should be able to append a paragraph without restating the whole text.
func TestDefaults_AreComposable(t *testing.T) {
	extra := "Ask #platform if the schema version surprises you."
	cmd := New(WithBinaryName(otherApp), WithLong(DefaultLong()+"\n\n"+extra))

	if !strings.Contains(cmd.Long, extra) {
		t.Error("appended paragraph missing")
	}
	if !strings.Contains(cmd.Long, "ldflags") {
		t.Error("default body lost when composing")
	}
}

func TestNew_ZeroOptionsUsesDefaults(t *testing.T) {
	cmd := New(WithBinaryName(otherApp))

	if cmd.Use != DefaultUse {
		t.Errorf("Use = %q, want %q", cmd.Use, DefaultUse)
	}
	if cmd.Short != DefaultShort() {
		t.Errorf("Short = %q, want the default", cmd.Short)
	}
	if cmd.Long != DefaultLong() {
		t.Errorf("Long = %q, want the default", cmd.Long)
	}
}

func TestCommand_JSONEnvelope(t *testing.T) {
	cmd := New(WithBinaryName(otherApp), WithComponents(Component{Name: "db_schema", Version: 7}))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--" + jsonFlag})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var env struct {
		Kind string `json:"kind"`
		Data Info   `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if env.Kind != KindVersionShow {
		t.Errorf("kind = %q, want %q", env.Kind, KindVersionShow)
	}
	if env.Data.Binary != otherApp {
		t.Errorf("binary = %q, want %q", env.Data.Binary, otherApp)
	}
	if env.Data.Source == "" {
		t.Error("source empty; it must always report how the version was determined")
	}
	if len(env.Data.Components) != 1 || env.Data.Components[0].Version != 7 {
		t.Errorf("components = %+v, want the one that was passed in", env.Data.Components)
	}
}

func TestCommand_RejectsArgs(t *testing.T) {
	cmd := New(WithBinaryName(otherApp))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"unexpected"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected an error for an unexpected positional argument, got nil")
	}
}

// TestCommitLabel_DirtyMarkerNeedsACommit covers the bug this caught in real
// output: "unknown (dirty)" asserts a dirty checkout for a build with no VCS
// record at all.
func TestCommitLabel_DirtyMarkerNeedsACommit(t *testing.T) {
	tests := []struct {
		name     string
		info     Info
		want     string
		wantDirt bool
	}{
		{"clean with commit", Info{Commit: "abc1234"}, "abc1234", false},
		{"dirty with commit", Info{Commit: "abc1234", Modified: true}, "", true},
		{"dirty without commit", Info{Commit: buildinfo.Unknown, Modified: true}, buildinfo.Unknown, false},
		{"dirty with empty commit", Info{Commit: "", Modified: true}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commitLabel(tt.info)
			if hasDirt := strings.Contains(got, "dirty"); hasDirt != tt.wantDirt {
				t.Errorf("commitLabel(%+v) = %q; dirty marker present = %v, want %v", tt.info, got, hasDirt, tt.wantDirt)
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("commitLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComponentLabelWidth(t *testing.T) {
	tests := []struct {
		name       string
		components []Component
		want       int
	}{
		{"none", nil, 0},
		{"single", []Component{{Name: "db"}}, 3},
		{"longest wins regardless of order", []Component{
			{Name: "config_schema"}, {Name: "db"},
		}, len("config_schema") + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := componentLabelWidth(tt.components); got != tt.want {
				t.Errorf("componentLabelWidth() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWriteText_AlignsComponents(t *testing.T) {
	var buf bytes.Buffer
	err := WriteText(&buf, Info{
		Binary: "acme", Version: "v1.0.0", Source: buildinfo.SourceLdflags,
		Components: []Component{{Name: "db", Version: 1}, {Name: "config_schema", Version: 2}},
	})
	if err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	var cols []int
	for _, line := range strings.Split(buf.String(), "\n") {
		if idx := strings.Index(line, " v"); strings.HasPrefix(line, "    ") && idx > 0 {
			cols = append(cols, idx)
		}
	}
	if len(cols) != 2 {
		t.Fatalf("expected 2 component lines, got %d:\n%s", len(cols), buf.String())
	}
	if cols[0] != cols[1] {
		t.Errorf("component versions misaligned (%d vs %d):\n%s", cols[0], cols[1], buf.String())
	}
}

func TestWriteText_NoComponentsOmitsHeading(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, Info{Binary: "acme", Version: "dev"}); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if strings.Contains(buf.String(), "components:") {
		t.Errorf("printed an empty components heading:\n%s", buf.String())
	}
}

// --- Collect ----------------------------------------------------------------

func TestCollect_CarriesAppSuppliedData(t *testing.T) {
	components := []Component{{Name: "db_schema", Version: 3}}

	info := Collect("probe", components...)

	if info.Binary != "probe" {
		t.Errorf("Binary = %q, want %q", info.Binary, "probe")
	}
	if len(info.Components) != 1 || info.Components[0] != components[0] {
		t.Errorf("Components = %+v, want %+v", info.Components, components)
	}
}

// TestCollect_PopulatesBuildinfoProjection asserts every field this command
// projects is actually carried over. buildinfo owns the values; the risk here is
// a field silently omitted from the projection, which no compiler catches.
func TestCollect_PopulatesBuildinfoProjection(t *testing.T) {
	bi := buildinfo.Get()
	info := Collect("probe")

	if info.Version != bi.Version {
		t.Errorf("Version = %q, want %q", info.Version, bi.Version)
	}
	if info.Source != bi.Source {
		t.Errorf("Source = %q, want %q", info.Source, bi.Source)
	}
	if info.Commit != bi.Commit {
		t.Errorf("Commit = %q, want %q", info.Commit, bi.Commit)
	}
	if info.BuildTime != bi.BuildTime {
		t.Errorf("BuildTime = %q, want %q", info.BuildTime, bi.BuildTime)
	}
	if info.GoVersion != bi.GoVersion {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, bi.GoVersion)
	}
	if want := bi.GOOS + "/" + bi.GOARCH; info.Platform != want {
		t.Errorf("Platform = %q, want %q", info.Platform, want)
	}
	if !info.Source.Valid() {
		t.Errorf("Source = %q is not a valid buildinfo.Source", info.Source)
	}
}

// TestInfo_ExcludesModuleList pins the deliberate omission: buildinfo.Info
// carries the full dependency list, and inlining it would bury the answer to
// "what is this binary" under every module it links.
func TestInfo_ExcludesModuleList(t *testing.T) {
	b, err := json.Marshal(Collect("probe"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"modules"`) {
		t.Errorf("version payload leaked the dependency list:\n%s", b)
	}
}

func TestInfo_HasCommit(t *testing.T) {
	tests := []struct {
		commit string
		want   bool
	}{
		{"abc1234", true},
		{buildinfo.Unknown, false},
		{"", false},
	}

	for _, tt := range tests {
		if got := (Info{Commit: tt.commit}).HasCommit(); got != tt.want {
			t.Errorf("Info{Commit: %q}.HasCommit() = %v, want %v", tt.commit, got, tt.want)
		}
	}
}

// --- fallback and error branches --------------------------------------------

// withArgs drives resolveBinaryName's fallbacks, which cannot be arranged
// through the public API without mutating process state.
func withArgs(t *testing.T, args []string) {
	t.Helper()
	prev := osArgs
	osArgs = func() []string { return args }
	t.Cleanup(func() { osArgs = prev })
}

func TestResolveBinaryName_Fallbacks(t *testing.T) {
	tests := []struct {
		name string
		give string
		args []string
		want string
	}{
		{"explicit name wins", "acme", []string{"/usr/bin/ignored"}, "acme"},
		{"falls back to the invoked executable", "", []string{"/usr/local/bin/acme"}, "acme"},
		// A bare "." or "/" basename is not a name; emitting it would render
		// help text that reads as a formatting bug.
		{"degenerate basename", "", []string{"."}, DefaultUse},
		{"root basename", "", []string{string(filepath.Separator)}, DefaultUse},
		{"empty argv entry", "", []string{""}, DefaultUse},
		{"no argv at all", "", nil, DefaultUse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withArgs(t, tt.args)
			if got := resolveBinaryName(tt.give); got != tt.want {
				t.Errorf("resolveBinaryName(%q) with argv %v = %q, want %q", tt.give, tt.args, got, tt.want)
			}
		})
	}
}

// TestCommand_HumanOutput covers the non-JSON branch of RunE, which the envelope
// tests never reach.
func TestCommand_HumanOutput(t *testing.T) {
	cmd := New(WithBinaryName(otherApp), WithComponents(Component{Name: "db_schema", Version: 2}))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{otherApp, "source:", "platform:", "db_schema:"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}

// failAfter is an io.Writer that fails on the nth write, so each error branch in
// WriteText can be reached individually.
type failAfter struct {
	n   int
	got int
}

func (f *failAfter) Write(p []byte) (int, error) {
	f.got++
	if f.got > f.n {
		return 0, errors.New("write failed")
	}
	return len(p), nil
}

// TestWriteText_PropagatesWriteErrors walks every write in WriteText. A renderer
// that swallows a failed write reports success while producing truncated output,
// which is worse than reporting the failure.
func TestWriteText_PropagatesWriteErrors(t *testing.T) {
	info := Info{
		Binary: otherApp, Version: "v1.0.0", Source: buildinfo.SourceLdflags,
		Commit: "abc1234", BuildTime: "2026-08-20T00:00:00Z",
		GoVersion: "go1.26.4", Platform: "darwin/arm64",
		Components: []Component{{Name: "db_schema", Version: 1}},
	}

	// Writes in order: the header, five fixed rows, the components heading, then
	// one row per component — eight for this Info.
	const totalWrites = 8
	for n := range totalWrites {
		t.Run(fmt.Sprintf("fails on write %d", n+1), func(t *testing.T) {
			if err := WriteText(&failAfter{n: n}, info); err == nil {
				t.Errorf("WriteText = nil error when write %d failed", n+1)
			}
		})
	}

	if err := WriteText(&failAfter{n: totalWrites}, info); err != nil {
		t.Errorf("WriteText = %v, want nil when every write succeeds", err)
	}
}
