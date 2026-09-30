package gobuild

import (
	"runtime"
	"strings"
	"testing"

	"github.com/khanakia/voltkit/apps/volt/buildmeta"
	"github.com/khanakia/voltkit/apps/volt/platform"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"
)

func TestPreflightPureGoAlwaysPasses(t *testing.T) {
	if err := Preflight(voltcfg.Config{}, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightCgoNoToolchainNamesTheFix(t *testing.T) {
	cfg := voltcfg.Config{CGO: true, Platforms: []string{"linux/amd64", "linux/arm64"}}
	err := Preflight(cfg, Options{})
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		// host target is fine; only arm64 should complain
		if err == nil || !strings.Contains(err.Error(), "linux/arm64") {
			t.Fatalf("want arm64 complaint, got %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "toolchain.cc") {
		t.Fatalf("error must name the fix (toolchain.cc): %v", err)
	}
}

func TestPreflightDarwinCgoFromNonDarwinRefused(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("host is darwin — the refusal only applies cross-OS")
	}
	cfg := voltcfg.Config{CGO: true, Platforms: []string{"darwin/arm64"},
		Toolchain: voltcfg.Toolchain{CC: "zig cc -target {{.ZigTarget}}"}}
	err := Preflight(cfg, Options{})
	if err == nil || !strings.Contains(err.Error(), "Apple SDK") {
		t.Fatalf("darwin cgo cross must be refused with the Apple SDK reason: %v", err)
	}
}

func TestPreflightNativeOnlySkipsToolchainDemands(t *testing.T) {
	cfg := voltcfg.Config{CGO: true, Platforms: []string{"linux/amd64", "darwin/arm64"}}
	if err := Preflight(cfg, Options{NativeOnly: true}); err != nil {
		t.Fatalf("--native-only needs no cross toolchain: %v", err)
	}
}

// TestCgoToolchainEnvLeavesTheNativeTargetAlone pins that the cross toolchain
// is applied to cross targets only. The native target builds with the host's
// compiler; handing it `zig cc -target <empty>` failed the one platform that
// needed no toolchain at all.
func TestCgoToolchainEnvLeavesTheNativeTargetAlone(t *testing.T) {
	cfg := voltcfg.Config{CGO: true, Toolchain: voltcfg.Toolchain{CC: "zig cc -target {{.ZigTarget}}", CXX: "zig c++ -target {{.ZigTarget}}"}}
	native := platform.Platform{OS: hostOS(), Arch: hostArch()}
	if env := cgoToolchainEnv(cfg, buildmeta.Vars{}, native); len(env) != 0 {
		t.Errorf("native target got a cross toolchain: %v", env)
	}
	// A cross target: linux on the architecture this host is not.
	other := "arm64"
	if hostArch() == "arm64" {
		other = "amd64"
	}
	cross := platform.Platform{OS: "linux", Arch: other}
	triple, ok := cross.ZigTarget()
	if !ok {
		t.Fatalf("no zig triple for %v", cross)
	}
	env := cgoToolchainEnv(cfg, buildmeta.Vars{ZigTarget: triple}, cross)
	if len(env) != 2 || env[0] != "CC=zig cc -target "+triple || env[1] != "CXX=zig c++ -target "+triple {
		t.Errorf("cross target env = %v", env)
	}
	// cgo off: nothing, whatever the toolchain says.
	cfg.CGO = false
	if env := cgoToolchainEnv(cfg, buildmeta.Vars{ZigTarget: triple}, cross); env != nil {
		t.Errorf("cgo off must set no compiler: %v", env)
	}
}
