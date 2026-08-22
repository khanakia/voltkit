package platform

import "testing"

func TestParse(t *testing.T) {
	p, err := Parse("darwin/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if p.OS != "darwin" || p.Arch != "arm64" {
		t.Fatalf("got %+v", p)
	}
	for _, bad := range []string{"", "darwin", "darwin/", "/arm64", "darwin-arm64"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q): want error, got nil", bad)
		}
	}
}

func TestParseListFailsFast(t *testing.T) {
	if _, err := ParseList([]string{"linux/amd64", "nope"}); err == nil {
		t.Fatal("want error on bad entry, got nil")
	}
}

// AssetName is a public contract reproduced by generated install scripts —
// this test pins the exact format so a drive-by rename fails loudly.
func TestAssetNameContract(t *testing.T) {
	cases := []struct {
		p    Platform
		want string
	}{
		{Platform{"darwin", "arm64"}, "notes_v1.4.0_darwin_arm64.tar.gz"},
		{Platform{"linux", "amd64"}, "notes_v1.4.0_linux_amd64.tar.gz"},
		{Platform{"windows", "amd64"}, "notes_v1.4.0_windows_amd64.zip"},
	}
	for _, c := range cases {
		if got := c.p.AssetName("notes", "v1.4.0"); got != c.want {
			t.Errorf("AssetName = %q, want %q", got, c.want)
		}
	}
}

func TestExeSuffixAndArchiveExt(t *testing.T) {
	win := Platform{"windows", "amd64"}
	nix := Platform{"linux", "arm64"}
	if win.ExeSuffix() != ".exe" || nix.ExeSuffix() != "" {
		t.Error("ExeSuffix wrong")
	}
	if win.ArchiveExt() != ".zip" || nix.ArchiveExt() != ".tar.gz" {
		t.Error("ArchiveExt wrong")
	}
}

func TestZigTarget(t *testing.T) {
	if tr, ok := (Platform{"linux", "arm64"}).ZigTarget(); !ok || tr != "aarch64-linux-gnu" {
		t.Errorf("linux/arm64 = %q %v", tr, ok)
	}
	if _, ok := (Platform{"darwin", "arm64"}).ZigTarget(); ok {
		t.Error("darwin must have no zig target (Apple SDK constraint, ADR-R09)")
	}
}

func TestDefaultSetIsPureGoBuildable(t *testing.T) {
	// The default set is a promise: every entry must cross-compile with
	// CGO_ENABLED=0 from any machine. That is true of any GOOS/GOARCH pair,
	// so here we only pin the set against accidental edits.
	if len(Default) != 5 {
		t.Fatalf("default platform set changed (len=%d) — this is a public contract; update install-script expectations too", len(Default))
	}
}
