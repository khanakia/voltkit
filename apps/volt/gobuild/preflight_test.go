package gobuild

import (
	"runtime"
	"strings"
	"testing"

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
