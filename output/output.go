// Package output renders the machine-readable side of every command.
//
// One envelope shape for all --json output, so a consumer can dispatch on
// `kind` and detect contract changes via `schema_version` without pattern
// matching on the payload. Commands that invent their own shape are the reason
// agent integrations break on upgrade.
//
// INVARIANT: data goes to stdout, everything else (progress, warnings, errors,
// hints) goes to stderr. `app list --json | jq` must work with no extra flags.
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// SchemaVersion is the version of the ENVELOPE, not of any payload. Bump it
// only when the envelope's own fields change — payload changes are described by
// the per-command contract instead.
const SchemaVersion = 1

// Envelope is the canonical wire format for --json.
//
// Count is omitted for single-object results so consumers do not have to
// distinguish "one row" from "count happens to be zero".
type Envelope struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Count         int    `json:"count,omitempty"`
	Data          any    `json:"data"`
}

// JSON writes one envelope to w.
//
// kind identifies the producing command ("version.show", "note.list") so a
// consumer piping several commands together can dispatch on a single field.
// count is meaningful only for listings; pass 0 for single results.
//
// A nil data is normalised to an empty slice: emitting `"data": null` is the
// single most common way a downstream script breaks, and there is no case where
// null carries information that [] does not.
func JSON(w io.Writer, kind string, data any, count int) error {
	if data == nil {
		data = []any{}
	}

	out, err := json.MarshalIndent(Envelope{
		SchemaVersion: SchemaVersion,
		Kind:          kind,
		Count:         count,
		Data:          data,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s envelope: %w", kind, err)
	}

	_, err = fmt.Fprintln(w, string(out))
	return err
}
