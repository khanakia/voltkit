// build_cmd.go — the `volt build` verb: cross-compile + archive + checksum,
// no network (spec, build-order step 1).
package main

import (
	"fmt"

	"github.com/khanakia/voltkit/apps/volt/gobuild"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"

	"github.com/spf13/cobra"
)

func newBuildCommand() *cobra.Command {
	var (
		version    string
		nativeOnly bool
		distDir    string
	)
	cmd := &cobra.Command{
		Use:   "build <dir>",
		Short: "Cross-compile a CLI for its platform matrix into dist/",
		Long: `Builds the package main in <dir> for every configured platform
(default: darwin/linux × amd64/arm64 + windows/amd64), stamps the version via
ldflags, archives each binary, and writes checksums.txt.

Reads optional per-directory config from <dir>/.volt.yml. No network, no
tokens, no git writes — safe to run anywhere.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			cfg, err := voltcfg.Load(dir)
			if err != nil {
				return err
			}
			res, err := gobuild.Run(gobuild.Options{
				Dir:        dir,
				Version:    version,
				NativeOnly: nativeOnly,
				DistDir:    distDir,
				Log:        cmd.OutOrStdout(),
			}, cfg)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d asset(s) + %s\n", len(res.Assets), res.Checksums)
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "version to stamp and to name assets with, e.g. v1.4.0 (required)")
	cmd.Flags().BoolVar(&nativeOnly, "native-only", false, "build only this machine's platform (skips are reported)")
	cmd.Flags().StringVar(&distDir, "dist", "", "artifact directory (default ./dist)")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}
