// Package platform models a GOOS/GOARCH build target.
//
// Why a package for two strings: the platform is the axis every build-time
// decision turns on — archive format (zip on windows, tar.gz elsewhere),
// binary suffix (.exe), zig target triples for cgo, and the asset names the
// install scripts must reproduce byte-for-byte. Centralising the parsing and
// derivations here is what lets the installer generator and the builder share
// one source of truth (see "The naming contract is the fragile part" in
// docsi/RELEASE_PIPELINE_SPEC.md).
package platform

import (
	"fmt"
	"strings"
)

// Platform is one GOOS/GOARCH pair, e.g. {darwin arm64}.
type Platform struct {
	OS   string
	Arch string
}

// Default is the platform set a repo gets with no config: every target a
// pure-Go build can produce from one machine. Kept small on purpose — a
// platform here is a promise the install scripts must honour forever.
// Overridable per repo via `platforms:` in .volt.yml.
var Default = []Platform{
	{OS: "darwin", Arch: "arm64"},
	{OS: "darwin", Arch: "amd64"},
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
}

// Parse converts "darwin/arm64" into a Platform. The slash form is the only
// accepted spelling — it matches GOOS/GOARCH order and the .volt.yml schema.
func Parse(s string) (Platform, error) {
	os, arch, ok := strings.Cut(s, "/")
	if !ok || os == "" || arch == "" {
		return Platform{}, fmt.Errorf("platform %q: want the form GOOS/GOARCH, e.g. darwin/arm64", s)
	}
	return Platform{OS: os, Arch: arch}, nil
}

// ParseList converts a .volt.yml `platforms:` list, failing on the first bad
// entry rather than silently dropping it — a skipped platform would surface
// months later as a user whose install finds no asset.
func ParseList(ss []string) ([]Platform, error) {
	out := make([]Platform, 0, len(ss))
	for _, s := range ss {
		p, err := Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// String renders the canonical "os/arch" spelling.
func (p Platform) String() string { return p.OS + "/" + p.Arch }

// ExeSuffix is ".exe" on windows and empty elsewhere.
func (p Platform) ExeSuffix() string {
	if p.OS == "windows" {
		return ".exe"
	}
	return ""
}

// ArchiveExt is the archive format users expect on each OS: zip on windows
// (built-in extraction), tar.gz elsewhere.
func (p Platform) ArchiveExt() string {
	if p.OS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// AssetName is the release-asset filename for a binary at a version:
// "<binary>_<version>_<os>_<arch><ext>", e.g. notes_v1.4.0_darwin_arm64.tar.gz.
//
// INVARIANT: this exact shape is reproduced by the generated install scripts.
// Changing it is a breaking change for every published installer — the reason
// it lives here, next to Platform, and nowhere else.
func (p Platform) AssetName(binary, version string) string {
	return fmt.Sprintf("%s_%s_%s_%s%s", binary, version, p.OS, p.Arch, p.ArchiveExt())
}

// zigArch maps GOARCH to zig's arch spelling. Only the pairs volt actually
// supports are listed; an unmapped pair is an error at the call site, not a
// silent guess — a wrong triple produces binaries for the wrong machine.
var zigArch = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
	"386":   "x86",
}

// zigOS maps GOOS to zig's OS/ABI segment for cgo cross-compilation.
// darwin is deliberately absent: cross-compiling cgo to macOS requires the
// Apple SDK, which cannot be redistributed (ADR-R09) — the pre-flight check
// reports that instead of letting zig fail obscurely.
var zigOS = map[string]string{
	"linux":   "linux-gnu",
	"windows": "windows-gnu",
}

// ZigTarget derives the `zig cc -target` triple ("aarch64-linux-gnu") for
// this platform, for the {{.ZigTarget}} template variable. ok=false means no
// supported triple exists (darwin, or an unmapped arch).
func (p Platform) ZigTarget() (string, bool) {
	a, okA := zigArch[p.Arch]
	o, okO := zigOS[p.OS]
	if !okA || !okO {
		return "", false
	}
	return a + "-" + o, true
}
