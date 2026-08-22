package gobuild

import "runtime"

// hostOS / hostArch are indirections over runtime.GOOS/GOARCH so tests can
// reason about --native-only without cross-OS CI.
func hostOS() string   { return runtime.GOOS }
func hostArch() string { return runtime.GOARCH }

// sortStrings is a tiny wrapper kept here so gobuild.go's imports stay
// focused; it exists only to make renderLDFlags' determinism explicit.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ { // insertion sort: n is single digits
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
