// preflight.go — the cgo mode decision (spec, "cgo — when one machine cannot
// build everything", ADR-R09).
//
// The mode is decided BEFORE any build starts: an unsatisfiable combination
// is a pre-flight error naming the fix, never a linker failure minutes in.
package gobuild

import (
	"fmt"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/platform"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"
)

// Preflight validates cfg's cgo/platform combination for the given build
// options. Returns nil when the build can proceed.
func Preflight(cfg voltcfg.Config, opts Options) error {
	if !cfg.CGO {
		return nil // mode A: pure Go — every platform builds anywhere
	}
	plats := platform.Default
	if len(cfg.Platforms) > 0 {
		var err error
		plats, err = platform.ParseList(cfg.Platforms)
		if err != nil {
			return err
		}
	}
	if opts.NativeOnly {
		return nil // mode C: only the host platform builds; no cross toolchain needed
	}
	var problems []string
	for _, p := range plats {
		switch {
		case p.OS == "darwin" && hostOS() != "darwin":
			// Never supported: Apple's SDK cannot be redistributed.
			problems = append(problems,
				fmt.Sprintf("%s: cgo cross-compilation to macOS requires the Apple SDK, which cannot be redistributed — build it on a macOS runner with --native-only (mode C in the spec)", p))
		case p.OS == hostOS() && p.Arch == hostArch():
			// native target: the host toolchain suffices
		case cfg.Toolchain.CC == "":
			problems = append(problems,
				fmt.Sprintf("%s: cgo is on but no C cross-compiler is configured — set toolchain.cc in .volt.yml (e.g. `zig cc -target {{.ZigTarget}}`), or build on a matching runner with --native-only", p))
		default:
			if _, ok := p.ZigTarget(); !ok && strings.Contains(cfg.Toolchain.CC, "ZigTarget") {
				problems = append(problems,
					fmt.Sprintf("%s: toolchain.cc uses {{.ZigTarget}} but no zig triple exists for this platform — add a per-platform override", p))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("cgo pre-flight failed:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
