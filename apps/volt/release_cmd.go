// release_cmd.go — `volt release`: tag, build, publish, verify (spec,
// build-order step 2; ordering per ADR-R10).
package main

import (
	"fmt"
	"os"

	"github.com/khanakia/voltkit/apps/volt/changelog"
	"github.com/khanakia/voltkit/apps/volt/publish"
	"github.com/khanakia/voltkit/apps/volt/release"
	"github.com/khanakia/voltkit/apps/volt/streams"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"

	"github.com/spf13/cobra"
)

func newReleaseCommand() *cobra.Command {
	var (
		fromTag       string
		snapshot      bool
		fromArtifacts bool
		strict        bool
		bump          string
	)
	cmd := &cobra.Command{
		Use:   "release [<dir> <version>]",
		Short: "Tag, build, publish and verify a release",
		Long: `The human path:      volt release ./cmd/notes v1.4.0
The CI path:         volt release --from-tag notes/v1.4.0   (tag already exists)
Dry run:             volt release <dir> --snapshot          (build all, publish nothing)

Order is fixed by design: tests → reserve tag → build → publish → verify.
Everything reversible happens before anything permanent; a failure after the
tag exists is recovered by re-running --from-tag (idempotent).`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			o := release.Options{
				Root:          ".",
				FromTag:       fromTag,
				Snapshot:      snapshot,
				FromArtifacts: fromArtifacts,
				Strict:        strict,
				WarmProxy:     publish.WarmProxy,
				Log:           cmd.OutOrStdout(),
			}
			switch {
			case fromTag != "":
				if len(args) != 0 {
					return fmt.Errorf("--from-tag takes no positional arguments")
				}
				// The tag's commit was tested when the tag was made; the
				// re-run exists to finish publishing, not to re-gate.
				o.SkipTests = true
			case snapshot && len(args) >= 1:
				o.Dir = args[0]
			case bump != "" && len(args) == 1:
				o.Dir = args[0]
				v, err := streams.Next(".", o.Dir, bump)
				if err != nil {
					return err
				}
				o.Version = v
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "bump %s: next version is %s\n", bump, v)
			case len(args) == 2:
				o.Dir, o.Version = args[0], args[1]
			default:
				return fmt.Errorf("usage: volt release <dir> <version> | volt release <dir> --bump patch|minor|major | volt release --from-tag <tag> | volt release <dir> --snapshot")
			}

			// The forge seam (docs/proposals/forge-provider.md): publisher
			// and repo identity come from the detected forge — command code
			// never names GitHub.
			f, err := detectForge(".")
			if err != nil {
				return err
			}
			o.Publisher = f.Publisher(".")
			repo := f.RepoOf(".")
			o.Repo = repo.String()
			if repo != "" {
				o.ChangelogURL = f.FileURL(repo, changelog.FileName)
			}
			// Brew config travels from the released directory's .volt.yml.
			if o.Dir != "" {
				if cfg, err := voltcfg.Load(o.Dir); err == nil {
					o.Brew = publish.BrewConfig(cfg.Brew)
				}
			}
			res, err := release.Run(o)
			for _, w := range res.Warnings {
				fmt.Fprintf(os.Stderr, "WARN: %s\n", w)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&fromTag, "from-tag", "", "(re)publish an existing tag — the CI/recovery path")
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "build everything, publish nothing, keep dist/")
	cmd.Flags().BoolVar(&fromArtifacts, "from-artifacts", false, "publish pre-built archives from dist/ (refuses an incomplete platform set)")
	cmd.Flags().BoolVar(&strict, "strict", false, "skipped channels and proxy warm-up failures become errors")
	cmd.Flags().StringVar(&bump, "bump", "", "compute the next version from the stream's newest tag: patch, minor or major")
	return cmd
}
