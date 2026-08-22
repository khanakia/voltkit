// Command demo is the reference binary assembled from the kit — the living
// proof the modules compose, and the smoke-test fixture volt builds in CI.
//
// Every command follows the lifecycle in docsi/ARCHITECTURE.md — parse,
// PersistentPreRunE, resolveContext, guard, work, render, exit. `version` is the
// worked reference; copy its shape.
//
// The binary carries no version variable of its own: provenance is resolved by
// the version module, which reads the -ldflags stamp when the release pipeline
// set one and otherwise falls back to the module version the Go toolchain
// records for `go install`. Adding a local `var version` here would shadow that
// and silently report "dev" on installed builds.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/khanakia/voltkit/apps/demo/appmeta"
	"github.com/khanakia/voltkit/versioncmd"

	"github.com/spf13/cobra"
)

func newRootCommand(meta appmeta.Meta) *cobra.Command {
	cmd := &cobra.Command{
		Use:   meta.Binary,
		Short: "demo — the voltkit reference CLI: kit modules wired into a working binary",
		Long: `volt is the reference binary for this CLI template.

Clone it, rename it, delete the demo entity, and start writing commands with
persistence, JSON output contracts, typed error codes, and a release pipeline
already wired.`,

		// Drives `volt --version`. Shares one resolution path with the version
		// subcommand so the two can never disagree.
		Version: versioncmd.Collect(meta.Binary).Version,

		// Errors are rendered by main so the exit code can be derived from the
		// error's own code, and so --json mode can emit a structured envelope
		// instead of cobra's plain-text default.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	registerCommands(cmd, meta)
	return cmd
}

func main() {
	// SIGINT/SIGTERM cancel the command context so long operations stop at a
	// safe point rather than being killed mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCommand(appmeta.Default)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		// P2 maps typed error codes onto the documented exit-code table
		// (0 ok / 1 runtime / 2 usage / 3 not-found / 4 conflict).
		os.Exit(1)
	}
}
