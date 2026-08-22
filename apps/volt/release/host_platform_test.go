package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"runtime"
	"testing"
)

// hostPlatformYML pins test builds to the host platform — the full matrix is
// gobuild's concern, and five cross-compiles per test would be waste.
func hostPlatformYML() string {
	return "platforms: [" + runtime.GOOS + "/" + runtime.GOARCH + "]\n"
}

// release_BrewConfig aliases the publish type so the big test file reads
// without an extra import line per literal.
type release_BrewConfig = struct {
	Tap         string `yaml:"tap"`
	Description string `yaml:"description"`
	License     string `yaml:"license"`
}

// tarList returns the file names inside a tar.gz.
func tarList(t *testing.T, raw []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = true
	}
	return out
}

// decompress returns the concatenated file contents of a tar.gz.
func decompress(t *testing.T, raw []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out bytes.Buffer
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			_, _ = out.ReadFrom(tr)
		}
	}
	return out.Bytes()
}
