# version

A plug-and-play `version` command, plus the build provenance behind it.

```go
import "github.com/khanakia/voltkit/versioncmd"
```

## Wiring it up

```go
root.AddCommand(version.New(
	version.WithBinaryName("acme"),
	version.WithComponents(
		version.Component{Name: "db_schema", Version: 1},
		version.Component{Name: "config_schema", Version: 1},
	),
	version.WithAliases("v"),
))
```

The name is optional. With no options at all, `version.New()` produces a working command that falls back to the running executable's own name.

## What it prints

```
$ acme version
acme v1.4.0
  source:      ldflags
  commit:      a3f1c2d
  built:       2026-08-20T09:25:16Z
  go:          go1.26.4
  platform:    darwin/arm64
  components:
    db_schema:     v1
    config_schema: v1
```

```sh
$ acme version --json | jq -r .data.source
ldflags
```

## Provenance — the `source` field

The version string alone is ambiguous. A binary reports `dev` both when someone built it locally **and** when a release pipeline silently failed to pass its ldflags. Those need very different responses, so `source` records which input actually won:

| `source` | Meaning |
|---|---|
| `ldflags` | stamped at build time by the release pipeline |
| `module` | resolved by `go install <pkg>@<version>` — no build flags needed |
| `unknown` | plain `go build` / `go run` / `go test` |

This comes from [`ubgo/buildinfo`](https://github.com/ubgo/buildinfo); this package projects it.

## Stamping a release

The `-X` target is buildinfo's own package path, not your `main`, so these flags are identical in every project and survive a rename:

```sh
go build -trimpath -ldflags="-s -w \
  -X github.com/ubgo/buildinfo.Version=$(git describe --tags --always) \
  -X github.com/ubgo/buildinfo.Commit=$(git rev-parse --short HEAD) \
  -X github.com/ubgo/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

## Component versions

Contract surfaces that move independently of the binary version — a patch release can still bump a schema. Integer, not semver: these are negotiation counters that only ever increment, and an integer cannot be mis-compared the way `"1.10"` against `"1.9"` can be under a string sort.

## Customising the text

Every user-visible string is overridable, and the defaults are exported as functions so you can **compose** rather than restate:

```go
version.New(
	version.WithBinaryName("acme"),
	version.WithLong(version.DefaultLong()+"\n\nAsk #platform if the schema version surprises you."),
)
```

| Option | Overrides |
|---|---|
| `WithBinaryName` | the name used in generated examples |
| `WithComponents` | app contract versions |
| `WithUse` / `WithAliases` | command name and alternatives |
| `WithShort` / `WithLong` / `WithExample` | help text |
| `WithHidden` | hide from help without removing the command |

Defaults: `DefaultUse`, `DefaultShort()`, `DefaultLong()`, `DefaultExample(binary)`.

## The name is data, not a literal

A library command must be able to **say** the app's name without **knowing** it. The name arrives as a parameter and is interpolated at construction, so one implementation serves every app:

```go
cmd := version.New(version.WithBinaryName("acme"))
// Examples show: acme version --json | jq -r .data.source
```

A test builds the command for a foreign app and asserts none of this template's identity survives anywhere in `Use`, `Short`, `Long`, or `Example`. A stray hardcoded name reads fine in this repo and only misleads users of a renamed project — who would never suspect the library.

Note the command asks for a **name**, not for an `appmeta.Meta`: it reads one field, so requiring the whole struct would force anyone wanting just this command to adopt the kit's config type. Callers holding a `Meta` pass `meta.Binary`. Contrast [`appdir`](../appdir), which genuinely reads five fields and so takes the struct.

## Reusing the pieces

```go
info := version.Collect("acme", components...)   // no cobra involved
version.WriteText(os.Stdout, info)               // the same human formatting
```

Useful when rendering version inside a larger `doctor`-style report.

`Info` is a **projection** of `buildinfo.Info`, not an embedding — embedding would inline buildinfo's `Modules` field, the full dependency list, into every `version --json`. A test asserts the payload never contains `"modules"`.

## Gotchas

`info.Modified` is only meaningful alongside a real commit. `HasCommit()` guards it — rendering `unknown (dirty)` asserts a dirty checkout for a build that carries no VCS record at all.

VCS stamps come from **commits**, not from the existence of `.git`. A repo with zero commits still reports `commit: unknown`.
