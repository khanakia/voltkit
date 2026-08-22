package publish

import (
	"strings"
	"testing"
)

// Fake is an in-memory Publisher for orchestrator tests (exported so the
// release package's tests can reuse it).
type Fake struct {
	Bodies map[string]string
	Assets map[string][]string
	// FailFirstCreate simulates a transient publish failure.
	FailFirstCreate bool
	created         int
}

func NewFake() *Fake {
	return &Fake{Bodies: map[string]string{}, Assets: map[string][]string{}}
}

func (f *Fake) ReleaseExists(tag string) bool { _, ok := f.Bodies[tag]; return ok }

func (f *Fake) CreateOrUpdate(tag, title, notesFile string, assets []string) error {
	f.created++
	if f.FailFirstCreate && f.created == 1 {
		return errTransient
	}
	body, err := readFile(notesFile)
	if err != nil {
		return err
	}
	f.Bodies[tag] = body
	names := f.Assets[tag]
	for _, a := range assets {
		names = append(names, base(a))
	}
	f.Assets[tag] = names
	return nil
}

func (f *Fake) FetchBody(tag string) (string, error)         { return f.Bodies[tag], nil }
func (f *Fake) FetchAssetNames(tag string) ([]string, error) { return f.Assets[tag], nil }

func TestVerifyCatchesMissingAssetAndBodyDrift(t *testing.T) {
	f := NewFake()
	notes, _ := WriteNotesFile("hello\n")
	if err := f.CreateOrUpdate("v1.0.0", "t", notes, []string{"/tmp/a.tar.gz"}); err != nil {
		t.Fatal(err)
	}
	// Clean verify.
	if p := Verify(f, "v1.0.0", "hello\n", []string{"a.tar.gz"}); len(p) != 0 {
		t.Fatalf("clean publish must verify: %v", p)
	}
	// Missing asset.
	p := Verify(f, "v1.0.0", "hello\n", []string{"a.tar.gz", "b.zip"})
	if len(p) != 1 || !strings.Contains(p[0], "b.zip") {
		t.Fatalf("missing asset not caught: %v", p)
	}
	// Body drift — the spliced-release-body incident class.
	f.Bodies["v1.0.0"] = "PASS ok something spliced in"
	if p := Verify(f, "v1.0.0", "hello\n", nil); len(p) == 0 {
		t.Fatal("body drift not caught")
	}
}

// CRLF and trailing-space differences from GitHub's round-trip must NOT read
// as drift.
func TestVerifyNormalizesLineEndings(t *testing.T) {
	f := NewFake()
	f.Bodies["v1"] = "line one\r\nline two  \r\n"
	if p := Verify(f, "v1", "line one\nline two\n", nil); len(p) != 0 {
		t.Fatalf("CRLF round-trip must verify clean: %v", p)
	}
}
