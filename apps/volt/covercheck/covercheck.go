// Package covercheck implements `volt cover`: test coverage for any repo —
// per module, merged into one honest total, renderable as a README badge.
//
// Self-contained on purpose: the badge is an SVG file committed to the repo,
// not a call to Codecov or shields.io — so it works for private repos, needs
// no account, and cannot rot when a third party changes an API.
package covercheck

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/khanakia/voltkit/apps/volt/cicheck"
)

// ModuleCoverage is one module's result.
type ModuleCoverage struct {
	Module  string  `json:"module"`
	Percent float64 `json:"percent"`
}

// Report is the full result of a coverage run.
type Report struct {
	Total   float64          `json:"total"`
	Modules []ModuleCoverage `json:"modules"`
}

// Run measures coverage for every module under root. Profiles are collected
// per module (a multi-module repo cannot be profiled in one `go test`
// invocation) and merged for the total, so the headline number weights every
// statement equally rather than averaging module percentages.
//
// progress receives a line as each module starts and finishes (with timing).
// It exists because a multi-module repo's test suites can run for minutes,
// and a command that prints nothing for minutes is indistinguishable from a
// hung one. nil → silent (tests use that).
func Run(root string, progress io.Writer) (Report, error) {
	var rep Report
	if progress == nil {
		progress = io.Discard
	}
	mods, err := cicheck.Modules(root)
	if err != nil {
		return rep, err
	}
	if len(mods) == 0 {
		return rep, fmt.Errorf("no Go modules found under %s", root)
	}
	tmp, err := os.MkdirTemp("", "volt-cover-*")
	if err != nil {
		return rep, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	var profiles []string
	for i, mod := range mods {
		_, _ = fmt.Fprintf(progress, "[%d/%d] testing %s ...", i+1, len(mods), mod)
		start := time.Now()
		profile := filepath.Join(tmp, fmt.Sprintf("mod%d.out", i))
		// -covermode=set explicitly: merged profiles must agree on mode, and
		// set is the cheapest — "was this statement reached" is what a badge
		// answers; counts belong to profiling, not gating.
		cmd := exec.Command("go", "test", "-covermode=set", "-coverprofile="+profile, "./...")
		cmd.Dir = filepath.Join(root, mod)
		if out, err := cmd.CombinedOutput(); err != nil {
			_, _ = fmt.Fprintf(progress, " FAILED\n")
			return rep, fmt.Errorf("go test in %s:\n%s", mod, strings.TrimSpace(string(out)))
		}
		pct, err := funcTotal(profile)
		if err != nil {
			return rep, fmt.Errorf("coverage of %s: %w", mod, err)
		}
		_, _ = fmt.Fprintf(progress, " %.1f%% (%.1fs)\n", pct, time.Since(start).Seconds())
		rep.Modules = append(rep.Modules, ModuleCoverage{Module: mod, Percent: pct})
		profiles = append(profiles, profile)
	}

	merged := filepath.Join(tmp, "merged.out")
	if err := mergeProfiles(merged, profiles); err != nil {
		return rep, err
	}
	rep.Total, err = funcTotal(merged)
	return rep, err
}

// mergeProfiles concatenates cover profiles: one mode line, then every
// statement line. Sound because module packages never overlap, and all
// profiles were produced with the same -covermode.
func mergeProfiles(dest string, profiles []string) error {
	var b strings.Builder
	b.WriteString("mode: set\n")
	for _, p := range profiles {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if line == "" || strings.HasPrefix(line, "mode:") {
				continue
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return os.WriteFile(dest, []byte(b.String()), 0o644)
}

// funcTotal computes the statement coverage of a profile by parsing it
// directly. NOT `go tool cover -func`: that command resolves source files
// through the current directory's module, so it fails on any profile from a
// different module — and always fails on a merged multi-module profile.
// The format is one block per line: "file:startL.C,endL.C numStmts hitCount";
// percent = statements-with-hits / statements.
//
// A profile with no statements (a module with no testable code) is 0, not an
// error — an empty module must not break the repo-wide number.
func funcTotal(profile string) (float64, error) {
	raw, err := os.ReadFile(profile)
	if err != nil {
		return 0, err
	}
	var total, covered int
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return 0, fmt.Errorf("malformed cover profile line %q in %s", line, profile)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, fmt.Errorf("cover profile %s: %w", profile, err)
		}
		total += n
		if fields[2] != "0" {
			covered += n
		}
	}
	if total == 0 {
		return 0, nil
	}
	// One decimal, matching `go tool cover -func` output so numbers agree
	// with what developers see from the plain toolchain.
	return float64(int(float64(covered)/float64(total)*1000+0.5)) / 10, nil
}

// Badge color thresholds: the conventional traffic light. Below Low is red,
// below Good is yellow, at or above Good is green — matching what readers
// assume from every other coverage badge they have seen.
const (
	ThresholdLow  = 50.0
	ThresholdGood = 75.0
)

func badgeColor(pct float64) string {
	switch {
	case pct >= ThresholdGood:
		return "#3fb950" // green
	case pct >= ThresholdLow:
		return "#d29922" // yellow
	default:
		return "#f85149" // red
	}
}

// BadgeSVG renders a flat "coverage | NN.N%" badge. Hand-rolled and
// dependency-free; widths are computed from character counts at the ~6.7px
// average of the 11px Verdana that badge services use.
func BadgeSVG(pct float64) string {
	label := "coverage"
	value := fmt.Sprintf("%.1f%%", pct)
	const charW = 6.7
	labelW := int(charW*float64(len(label))) + 12
	valueW := int(charW*float64(len(value))) + 12
	total := labelW + valueW
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">
  <linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
  <clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="%d" height="20" fill="#555"/>
    <rect x="%d" width="%d" height="20" fill="%s"/>
    <rect width="%d" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
    <text x="%d" y="14">%s</text>
    <text x="%d" y="14">%s</text>
  </g>
</svg>
`, total, label, value, total, labelW, labelW, valueW, badgeColor(pct), total,
		labelW/2, label, labelW+valueW/2, value)
}
