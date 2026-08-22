package main

import "testing"

// The dev-build guard must catch BOTH dev shapes — "v0.0.0-dev.<sha>" from
// task install AND the bare ldflags default "dev" from `go build` — or
// update clobbers a working-tree binary (live incident, 2026-08-22).
func TestIsDevBuild(t *testing.T) {
	cases := map[string]bool{
		"dev":                    true,
		"v0.0.0-dev.d317986":     true,
		"v0.0.0-dev.abc.dirty":   true,
		"v0.5.0":                 false,
		"v0.5.0-rc.1":            false,
		"v0.0.0-snapshot-abc123": false, // snapshots are named artifacts, not working trees
	}
	for v, want := range cases {
		if got := isDevBuild(v); got != want {
			t.Errorf("isDevBuild(%q) = %v, want %v", v, got, want)
		}
	}
}
