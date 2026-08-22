// status_cmd.go — `volt status`: what is releasable, where each stream
// stands, and what needs releasing.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/khanakia/voltkit/apps/volt/streams"

	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Per-stream release state: last tag, commits since, suggested next version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			list, err := streams.Discover(".")
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			for _, s := range list {
				switch {
				case s.LastVersion == "":
					_, _ = fmt.Fprintf(out, "%-16s %-8s unreleased            → volt release %s --bump patch starts at %s\n",
						s.Dir, s.Kind, s.Dir, streams.FirstVersion)
				case s.CommitsAhead == 0:
					_, _ = fmt.Fprintf(out, "%-16s %-8s %-9s up to date\n", s.Dir, s.Kind, s.LastVersion)
				default:
					_, _ = fmt.Fprintf(out, "%-16s %-8s %-9s %d commit(s) since  → suggest %s\n",
						s.Dir, s.Kind, s.LastVersion, s.CommitsAhead, s.Suggested)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}
