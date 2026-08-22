package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture builds a local template root (no network): _base + one variant.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("volt-templates.yml", "version: 1\ndefault: minimal\nvariants:\n  minimal:\n    description: d\n")
	write("templates/_base/go.mod.tmpl", "module [[.Module]]\n\ngo 1.22\n")
	// .volt.yml contains Go-template {{ }} that must pass through UNRENDERED.
	write("templates/_base/.volt.yml.tmpl", "binary: [[.Name]]\nldflags:\n  vars:\n    main.version: \"{{.Version}}\"\n")
	write("templates/_base/shared.txt", "verbatim, no rendering: [[.Name]] stays literal\n")
	write("templates/minimal/main.go.tmpl", "package main\n\n// [[.Name]] scaffold\nfunc main() {}\n")
	// The variant REPLACES a _base file — the overlay semantic.
	write("templates/minimal/shared.txt", "overridden by variant\n")
	return root
}

var vars = Vars{Name: "mytool", Module: "example.test/mytool", Variant: "minimal",
	Ref: "v0.1.0", TemplateRepo: "khanakia/volt-cli", TemplateCommit: "abc", VoltVersion: "dev", At: "t"}

func TestGenerateRendersAndOverlays(t *testing.T) {
	root := fixture(t)
	dest := filepath.Join(t.TempDir(), "mytool")
	files, err := Generate(root, "minimal", dest, vars)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files written")
	}
	read := func(rel string) string {
		raw, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return string(raw)
	}
	// .tmpl rendered, suffix stripped.
	if got := read("go.mod"); !strings.Contains(got, "module example.test/mytool") {
		t.Errorf("go.mod not rendered: %q", got)
	}
	// [[ ]] rendered; {{ }} preserved verbatim.
	voltYML := read(".volt.yml")
	if !strings.Contains(voltYML, "binary: mytool") || !strings.Contains(voltYML, "{{.Version}}") {
		t.Errorf(".volt.yml wrong: %q", voltYML)
	}
	// Non-.tmpl copies verbatim — [[.Name]] must stay literal...
	// ...except this one was overridden by the variant layer.
	if got := read("shared.txt"); got != "overridden by variant\n" {
		t.Errorf("variant overlay must win: %q", got)
	}
}

func TestGenerateRefusesExistingDir(t *testing.T) {
	root := fixture(t)
	dest := t.TempDir() // exists
	if _, err := Generate(root, "minimal", dest, vars); err == nil {
		t.Fatal("must never overwrite an existing directory")
	}
}

func TestGenerateUnknownVariant(t *testing.T) {
	root := fixture(t)
	_, err := Generate(root, "ghost", filepath.Join(t.TempDir(), "x"), vars)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("unknown variant must error naming it: %v", err)
	}
}

func TestLoadMeta(t *testing.T) {
	m, err := LoadMeta(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if m.Default != "minimal" || len(m.Variants) != 1 {
		t.Fatalf("meta: %+v", m)
	}
}

// A template referencing an unknown variable must fail loudly, not render
// empty (same rule as ldflags vars).
func TestGenerateUnknownVarFails(t *testing.T) {
	root := fixture(t)
	p := filepath.Join(root, "templates/minimal/bad.txt.tmpl")
	if err := os.WriteFile(p, []byte("[[.DoesNotExist]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, "minimal", filepath.Join(t.TempDir(), "y"), vars); err == nil {
		t.Fatal("unknown template variable must error")
	}
}
