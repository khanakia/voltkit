// doctor_cmd.go — `volt doctor`: check the repo is releasable BEFORE it
// matters. Read-only; every finding names its fix.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/khanakia/voltkit/apps/volt/gitx"

	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check this repo is releasable before it matters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			warn := 0
			check := func(ok bool, good, bad string) {
				if ok {
					_, _ = fmt.Fprintf(out, "  ok    %s\n", good)
				} else {
					warn++
					_, _ = fmt.Fprintf(out, "  WARN  %s\n", bad)
				}
			}

			_, goErr := exec.LookPath("go")
			check(goErr == nil, "go toolchain on PATH", "go not found — nothing can build")
			_, gitErr := exec.LookPath("git")
			check(gitErr == nil, "git on PATH", "git not found — tags are the version source")
			_, lintErr := exec.LookPath("golangci-lint")
			check(lintErr == nil, "golangci-lint on PATH", "golangci-lint not installed — volt ci skips lint (loudly)")

			dirty, err := gitx.IsDirty(".")
			check(err == nil && !dirty, "working tree clean", "working tree dirty — releases will refuse")

			// A repo without an origin remote can build but never publish;
			// catching it here beats a mid-release failure.
			_, remoteErr := exec.Command("git", "remote", "get-url", "origin").Output()
			check(remoteErr == nil, "origin remote configured", "no origin remote — releases cannot push tags or publish")

			// Forge probes (auth, CLI presence) come from the seam — doctor
			// prints any forge's checks without knowing what they are.
			f, ferr := detectForge(".")
			if ferr != nil {
				return ferr
			}
			for _, c := range f.Doctor() {
				check(c.OK, c.Good, c.Bad)
			}

			// A Go version literal in a workflow drifts from go.mod (spec,
			// "Soft conventions").
			pinned := pinnedGoVersions(".github/workflows")
			check(len(pinned) == 0, "no Go version literals in workflows",
				fmt.Sprintf("workflow(s) pin a Go version literal (%s) — use go-version-file: go.mod", strings.Join(pinned, ", ")))

			// Homebrew channel credential — gated, not required.
			if os.Getenv("HOMEBREW_TAP_GITHUB_TOKEN") == "" {
				_, _ = fmt.Fprintf(out, "  note  HOMEBREW_TAP_GITHUB_TOKEN unset — the Homebrew channel (when built) will be skipped\n")
			}

			if warn > 0 {
				return fmt.Errorf("%d finding(s)", warn)
			}
			_, _ = fmt.Fprintln(out, "all clear")
			return nil
		},
	}
}

// pinnedGoVersions finds `go-version:` literals in workflow files — the
// drift-prone pattern go-version-file exists to replace.
func pinnedGoVersions(dir string) []string {
	var hits []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "go-version:") && !strings.Contains(t, "go-version-file") {
				hits = append(hits, e.Name())
				break
			}
		}
	}
	return hits
}
