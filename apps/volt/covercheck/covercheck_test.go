package covercheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Two modules: one fully tested, one untested — the merged total must weight
// statements, not average percentages (100+0 averaged would be 50; the real
// total depends on statement counts).
func TestRunMultiModuleMergedTotal(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/go.mod", "module example.test/a\n\ngo 1.22\n")
	write(t, root, "a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	write(t, root, "a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { if A() != 1 { t.Fail() } }\n")
	write(t, root, "b/go.mod", "module example.test/b\n\ngo 1.22\n")
	write(t, root, "b/b.go", "package b\n\nfunc B() int { return 2 }\n\nfunc C() int { return 3 }\n\nfunc D() int { return 4 }\n")
	write(t, root, "b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestNothing(t *testing.T) {}\n")

	var progress strings.Builder
	rep, err := Run(root, &progress)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Modules) != 2 {
		t.Fatalf("modules: %+v", rep.Modules)
	}
	if rep.Modules[0].Percent != 100.0 {
		t.Errorf("module a = %.1f, want 100", rep.Modules[0].Percent)
	}
	if rep.Modules[1].Percent != 0.0 {
		t.Errorf("module b = %.1f, want 0", rep.Modules[1].Percent)
	}
	// 1 covered of 4 statements = 25%, NOT the 50% a naive average gives.
	if rep.Total != 25.0 {
		t.Errorf("total = %.1f, want statement-weighted 25.0", rep.Total)
	}
	// Progress must name each module with its position — a silent minutes-long
	// run is indistinguishable from a hung one.
	for _, want := range []string{"[1/2] testing a", "[2/2] testing b", "100.0%"} {
		if !strings.Contains(progress.String(), want) {
			t.Errorf("progress missing %q:\n%s", want, progress.String())
		}
	}
}

func TestBadgeSVGColorsAndContent(t *testing.T) {
	cases := []struct {
		pct   float64
		color string
	}{
		{90, "#3fb950"}, {75, "#3fb950"}, {60, "#d29922"}, {50, "#d29922"}, {20, "#f85149"},
	}
	for _, c := range cases {
		svg := BadgeSVG(c.pct)
		if !strings.Contains(svg, c.color) {
			t.Errorf("%.0f%%: want color %s", c.pct, c.color)
		}
		if !strings.Contains(svg, "coverage") || !strings.Contains(svg, "%") {
			t.Errorf("badge text missing: %s", svg[:80])
		}
	}
}
