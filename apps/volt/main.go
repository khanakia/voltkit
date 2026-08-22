// Command volt is the Volt family tool: scaffold, gate, build, release and
// deploy any project in the family — and, by design, any Go repo at all
// (ADR-R08: no voltkit imports here, ever).
//
// Design spec: docsi/RELEASE_PIPELINE_SPEC.md. Verbs are universal — the
// project kind is detected, never typed (ADR-R12). Unknown subcommands fall
// through to `volt-<name>` on PATH (the kubectl/git plugin model), which is
// wired now, before any plugin exists, because retrofitting dispatch changes
// argument parsing.
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func main() {
	root := newRootCommand()
	root.AddCommand(newBuildCommand())
	root.AddCommand(newReleaseCommand())
	root.AddCommand(newCICommand())
	root.AddCommand(newGenCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newCoverCommand())
	root.AddCommand(newStatusCommand())
	root.AddCommand(newUpdateCommand())
	root.AddCommand(newNewCommand())
	root.AddCommand(newUpgradeCommand())

	// Plugin dispatch: if the first arg is not a builtin, try volt-<arg>.
	// Checked before cobra runs so cobra's "unknown command" error is the
	// fallback, not the first answer.
	if len(os.Args) > 1 {
		if path, ok := pluginPath(root, os.Args[1]); ok {
			os.Exit(runPlugin(path, os.Args[2:]))
		}
	}

	if err := root.Execute(); err != nil {
		// cobra already printed the error; a non-zero exit is the contract.
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "volt",
		Version: voltVersion + " (" + voltCommit + ")",
		Short:   "volt — build, release and ship Volt-family projects (and any Go repo)",
		Long: `volt is the Volt family tool. One vocabulary everywhere:

  volt ci                                   the gate: fmt, vet, lint, race tests
  volt build ./cmd/notes --version v1.4.0   cross-compile + archive + checksums
  volt release ./cmd/notes v1.4.0           tag, build, publish, verify
  volt release --from-tag notes/v1.4.0      (re)publish an existing tag — the CI path
  volt gen                                  write workflow stubs + install scripts
  volt doctor                               is this repo releasable?
  (new, deploy — coming per the build order in the spec)

Unknown subcommands run volt-<name> from PATH (kubectl-style plugins).`,
		SilenceUsage: true, // a failed build should print its error, not the help text
	}
}

// pluginPath reports whether arg names a plugin rather than a builtin:
// not a registered subcommand, not a flag, and volt-<arg> exists on PATH.
func pluginPath(root *cobra.Command, arg string) (string, bool) {
	if arg == "" || arg[0] == '-' {
		return "", false
	}
	for _, c := range root.Commands() {
		if c.Name() == arg || c.HasAlias(arg) {
			return "", false
		}
	}
	// cobra's built-ins (help, completion) are registered lazily; treat the
	// two documented names as builtins explicitly so a stray volt-help on
	// PATH can never shadow them.
	if arg == "help" || arg == "completion" {
		return "", false
	}
	path, err := exec.LookPath("volt-" + arg)
	if err != nil {
		return "", false
	}
	return path, true
}

// runPlugin executes the plugin with volt's stdio, returning its exit code —
// the plugin owns the whole interaction from here.
func runPlugin(path string, args []string) int {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "volt: plugin %s: %v\n", path, err)
		return 1
	}
	return 0
}
