# output

The machine-readable side of every command: one stable JSON envelope.

```go
import "github.com/khanakia/voltkit/output"
```

## Why one envelope

Consumers — scripts, agents, a future MCP server — need to detect shape changes without regexing. Commands that invent their own shape are the reason agent integrations break on upgrade.

```json
{
  "schema_version": 1,
  "kind": "note.list",
  "count": 2,
  "data": [ ... ]
}
```

| Field | Purpose |
|---|---|
| `schema_version` | the **envelope's** version, not the payload's. Bump only when these fields change |
| `kind` | which command produced this (`note.list`, `version.show`), so a consumer piping several commands can dispatch on one field |
| `count` | omitted for single-object results, so "one row" is never confused with "count is zero" |
| `data` | the payload. Never `null` |

## Usage

```go
func run(cmd *cobra.Command, args []string) error {
	notes, err := store.List(cmd.Context())
	if err != nil {
		return err
	}
	if jsonOut {
		return output.JSON(cmd.OutOrStdout(), "note.list", notes, len(notes))
	}
	return renderTable(cmd.OutOrStdout(), notes)
}
```

Single result — pass `0` for count:

```go
output.JSON(cmd.OutOrStdout(), "note.show", note, 0)
```

## The two rules

**`data` is never `null`.** A nil payload is normalised to `[]`. Emitting `"data": null` is the single most common way a downstream script breaks, and there is no case where `null` carries information `[]` does not.

**stdout is data, stderr is diagnostics.** Every progress spinner, warning, hint, and log line goes to stderr, so this works with no extra flags:

```sh
acme note list --json | jq -r '.data[].id'
```

Violating this once poisons the whole tool.

## Writer, not stdout

`JSON` takes an `io.Writer` and commands pass `cmd.OutOrStdout()`. That keeps output capturable in tests and redirectable by callers:

```go
var buf bytes.Buffer
output.JSON(&buf, "note.show", note, 0)
```

## Errors

A marshal failure returns an error naming the kind, and writes **nothing** — a truncated envelope on stdout is worse than no envelope:

```go
if err := output.JSON(w, "note.list", payload, n); err != nil {
	// "marshal note.list envelope: json: unsupported type: func()"
}
```
