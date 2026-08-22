// update_cmd.go — `volt update`: self-update from voltkit's own volt/*
// releases, checksum-verified. Closes volt's own propagation loop: the fleet
// should not update its tool by hand.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"strings"

	"github.com/khanakia/voltkit/apps/volt/forge"

	"github.com/spf13/cobra"
)

// voltRepo is where volt's own releases live. Its release tags are
// volt/vX.Y.Z (rule one: CLI tag = binary name).
const voltRepo = "khanakia/voltkit"

func newUpdateCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update this binary to the newest released volt",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// A dev build is someone's working tree — clobbering it with a
			// release would silently discard what they are testing.
			if isDevBuild(voltVersion) && !force {
				return fmt.Errorf("this is a dev build (%s) — it tracks a working tree, not releases; use --force to replace it anyway", voltVersion)
			}
			tag, err := latestVoltTag()
			if err != nil {
				return err
			}
			bare := strings.TrimPrefix(tag, "volt/")
			if bare == voltVersion {
				_, _ = fmt.Fprintf(out, "already at %s\n", voltVersion)
				return nil
			}
			_, _ = fmt.Fprintf(out, "updating %s → %s\n", voltVersion, bare)

			self, err := os.Executable()
			if err != nil {
				return err
			}
			self, _ = filepath.EvalSymlinks(self)
			if err := downloadAndReplace(tag, bare, self); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "updated %s\n", self)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace even a dev build")
	return cmd
}

// isDevBuild reports whether a stamped version names a working-tree build.
// Both shapes are real: task-install stamps "v0.0.0-dev.<sha>[.dirty]", while
// a bare `go build` leaves the ldflags default "dev" — the guard must catch
// BOTH or `volt update` silently clobbers someone's working tree (found
// live 2026-08-22: a plain-"dev" binary updated without --force).
func isDevBuild(v string) bool {
	return v == "dev" || strings.Contains(v, "-dev")
}

// latestVoltTag finds the newest volt/* release. The repo-wide
// /releases/latest cannot be used: voltkit is a monorepo, so "latest" may be
// a library's release.
func latestVoltTag() (string, error) {
	// forge.GitHub constructed, not detected: volt's own releases live on
	// GitHub by definition (voltRepo).
	tags, err := (forge.GitHub{}).ReleaseTags(forge.Repo(voltRepo), "volt/")
	if err != nil {
		return "", err
	}
	if len(tags) == 0 {
		return "", fmt.Errorf("no volt/* release found in %s", voltRepo)
	}
	return tags[0], nil
}

// downloadAndReplace fetches the platform asset, verifies its checksum, and
// atomically swaps the running executable (write-sibling + rename — safe on
// every Unix; the running process keeps its old inode).
func downloadAndReplace(tag, bare, self string) error {
	asset := fmt.Sprintf("volt_%s_%s_%s.tar.gz", bare, runtime.GOOS, runtime.GOARCH)
	// volt's own releases live on GitHub by definition (voltRepo), so the
	// forge is constructed, not detected — the URL shape still comes from
	// the seam, never inline.
	home := forge.GitHub{}
	tmp, err := os.MkdirTemp("", "volt-update-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	for _, f := range []string{asset, "checksums.txt"} {
		if err := run(tmp, "curl", "-fsSL", "-o", f, home.AssetURL(forge.Repo(voltRepo), tag, f)); err != nil {
			return err
		}
	}
	// shasum, not sha256sum: stock macOS has only the former (same lesson as
	// install.sh, 2026-08-22).
	sumCmd := "sha256sum"
	if _, err := exec.LookPath(sumCmd); err != nil {
		sumCmd = "shasum"
	}
	check := fmt.Sprintf(`grep " %s$" checksums.txt | %s -a 256 -c -`, asset, sumCmd)
	if sumCmd == "sha256sum" {
		check = fmt.Sprintf(`grep " %s$" checksums.txt | sha256sum -c -`, asset)
	}
	if err := run(tmp, "sh", "-c", check); err != nil {
		return fmt.Errorf("checksum verification FAILED for %s — refusing to install", asset)
	}
	if err := run(tmp, "tar", "xzf", asset); err != nil {
		return err
	}
	staged := self + ".new"
	if err := os.Rename(filepath.Join(tmp, "volt"), staged); err != nil {
		// cross-device rename fails; fall back to copy into place
		if err := run(tmp, "cp", filepath.Join(tmp, "volt"), staged); err != nil {
			return err
		}
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return err
	}
	return os.Rename(staged, self)
}

func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}
