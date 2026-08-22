package changelog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# Changelog

## [1.4.0] - 2026-08-21

### Added
- the thing

## [1.3.0] - 2026-08-01

- older stuff
`

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNotesExtractsExactSection(t *testing.T) {
	dir := write(t, sample)
	got := Notes(dir, "notes/v1.4.0", "1.4.0", "https://github.com/khanakia/notes/blob/main/CHANGELOG.md")
	if !strings.Contains(got, "the thing") {
		t.Fatalf("section content missing: %q", got)
	}
	if strings.Contains(got, "older stuff") {
		t.Fatalf("bled into the next section: %q", got)
	}
}

func TestNotesFallsBackWhenMissing(t *testing.T) {
	for name, dir := range map[string]string{
		"no file":       t.TempDir(),
		"no section":    write(t, "# Changelog\n\n## [9.9.9]\n\nx\n"),
		"blank section": write(t, "# Changelog\n\n## [1.4.0]\n\n   \n\n## [1.3.0]\n\nx\n"),
	} {
		got := Notes(dir, "notes/v1.4.0", "1.4.0", "https://github.com/khanakia/notes/blob/main/CHANGELOG.md")
		if !strings.Contains(got, "Release `notes/v1.4.0`") {
			t.Errorf("%s: fallback not used: %q", name, got)
		}
		if !strings.Contains(got, "CHANGELOG.md") {
			t.Errorf("%s: fallback must link the changelog: %q", name, got)
		}
	}
}
