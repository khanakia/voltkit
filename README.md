# voltkit

Build, release and ship Go CLIs and libraries — **any repo layout, one command** — plus a kit of importable modules for building the CLIs themselves.

Two things live here:

1. **The `volt` tool** (`apps/volt`) — scaffolding, CI gate, coverage, cross-platform builds, tagged releases with verification, generated workflows and install scripts.
2. **The kit modules** — importable packages (`versioncmd`, `appdir`, `output`, …) a Go CLI adopts one at a time via `go get`.

## The volt tool

```sh
brew install khanakia/tap/volt        # or: curl release archive, or task install from source
```

| Command | Does |
|---|---|
| `volt new cli <name>` | scaffold from the [volt-cli templates](https://github.com/khanakia/volt-cli) (`--template cobra`, `--list`) |
| `volt status` | per-stream release state: last tag, commits since, suggested next version |
| `volt ci [dir]` | the gate: gofmt, vet, golangci-lint (embedded config), race tests — changed modules by default; `--fix` applies auto-fixes |
| `volt cover [dir]` | coverage per module + statement-weighted total; `--badge coverage.svg` writes a self-contained README badge, `--check` fails when it is stale, `--min N` is a CI floor |
| `volt build <dir> --version vX.Y.Z` | cross-compile matrix → archives + `checksums.txt`; ldflags stamping verified in the produced binary |
| `volt release <dir> <version>` | tests → reserve tag → build → publish → **verify the published artifact**; `--bump patch\|minor\|major`, `--snapshot` (publish nothing), `--from-tag` (CI/recovery, idempotent) |
| `volt gen` | write workflow stubs + install scripts, hash-guarded: hand-edited files are refused with a diff, never clobbered |
| `volt gen skills` | wire [`skillcmd`](./skillcmd) into a project: guarded wiring file + a one-time starter skill; releases then attach `skills_<version>.tar.gz` automatically |
| `volt doctor` | is this repo releasable? tools, auth, remote, version-pin drift |
| `volt update` | self-update from volt's own releases, checksum-verified |
| `volt upgrade` | re-apply template evolution to a scaffolded project — three-way merge, your edits survive |

### The tag grammar — one rule, every layout

Libraries tag by **directory path** (the Go module proxy resolves no other shape); CLIs tag by **binary name**; a single-thing repo root tags bare:

```
v1.4.0                 the repo root
notes/v1.4.0           a CLI at cmd/notes (tag = binary name, folder depth irrelevant)
pkg/textutil/v0.1.0    a library submodule (tag = directory path — proxy-mandated)
```

Works identically on a single-CLI repo, a library repo, a monorepo of CLIs, and mixed repos — proven live by the demo fixtures: [volt-demo-cli](https://github.com/khanakia/volt-demo-cli), [volt-demo-lib](https://github.com/khanakia/volt-demo-lib), [volt-demo-clis](https://github.com/khanakia/volt-demo-clis).

### `.volt.yml` — optional, per released directory

```yaml
binary: mycli                      # default: the directory name
platforms: [darwin/arm64, linux/amd64, windows/amd64]
ldflags:
  vars:                            # REPLACES the default stamp map
    main.version: "{{.Version}}"
    main.commit: "{{.ShortCommit}}"
brew:
  tap: khanakia/homebrew-tap
  description: "One-line description"
internal: true                     # never released: hidden from status, refused by
                                   # release — for workspace-local plumbing modules
hooks:                             # your own scripts around the release
  pre_release: ./scripts/gate.sh   #   before the tag — non-zero aborts, nothing created
  post_release: ./scripts/notify.sh#   after publish+verify — promote, announce, mirror
```

Everything volt can detect — kind (CLI vs library via `go list`), module path, binary name — is detected, never configured. Full schema and design: [`apps/volt/README.md`](./apps/volt/README.md).

### A release, end to end

```sh
volt status                        # what needs releasing?
volt release ./cmd/notes --bump patch
# testing cmd/notes
# reserved tag notes/v0.2.1
# built notes_v0.2.1_darwin_arm64.tar.gz ... (5 platforms)
# released notes/v0.2.1 — 6 asset(s), verified
```

Publishing is idempotent (`--from-tag` re-runs a half-finished release), channels are credential-gated (a missing Homebrew token skips loudly, never fails), and every publish re-reads what it produced — the release body, the checksums, the proxy entry — rather than trusting exit codes.

## The kit — importable modules

Every new CLI re-decides the same things: where the database file lives, what `--json` output looks like, how the version gets stamped. Those decisions are made once here, as **importable modules** — adopt one at a time with `go get`, pick up improvements with `go get -u`.

```go
// a ready-made `version` subcommand with build provenance:
root.AddCommand(versioncmd.New(versioncmd.WithBinaryName("mycli")))
```

```go
// the stable JSON envelope every command's --json emits:
output.JSON(os.Stdout, "notes.list", notes, len(notes))
// → {"schema_version":1,"kind":"notes.list","count":2,"data":[...]}
```

```go
// where does state live? resolved by policy, never hardcoded:
paths, _ := appdir.Resolve(meta)   // reports WHICH rung decided, too
```

## Modules

| Module | Import | What it does |
|---|---|---|
| **root** | `github.com/khanakia/voltkit/...` | the kit's core packages, below |
| [`appmeta`](./apps/demo/appmeta) | `.../appmeta` | app identity: name, env prefix, directory names, DB location policy |
| [`appdir`](./appdir) | `.../appdir` | resolves where state lives — and reports which rung decided |
| [`output`](./output) | `.../output` | the stable `{schema_version, kind, count, data}` JSON envelope |
| [`versioncmd`](./versioncmd) | `.../versioncmd` | plug-and-play `version` command with build provenance |
| [`skillcmd`](./skillcmd) | `.../skillcmd` | a `skills` subcommand for any CLI: serves the project's published SKILL.md agent skills, always matched to the binary's version |
| [`apps/demo`](./apps/demo) | — | the reference binary built from the kit — installable, not importable |
| [`apps/volt`](./apps/volt) | — | the family tool: `new` · `status` · `ci` · `cover` · `build` · `release` · `gen` · `doctor` · `update` — see its [README](./apps/volt/README.md) |

Command modules are flat siblings of the root — `doctorcmd`, `configcmd`, and `backupcmd` will land beside `versioncmd` (command modules carry the `-cmd` suffix — see CONTRIBUTING), each independently versioned and optional. Each module's own README covers its API and usage.

## Contributing

Building, testing, repo layout, design rules and the dev loop live in [CONTRIBUTING.md](./CONTRIBUTING.md).


## Docs

| | |
|---|---|
| Each module's `README.md` | how to use that module |
| [`docs/roadmap.md`](./docs/roadmap.md) | what is deferred and what is planned |
| [`docs/proposals/`](./docs/proposals) | designs worked out in full but not built, including why |

## Related

[`ubgo/buildinfo`](https://github.com/ubgo/buildinfo) supplies the build provenance that [`versioncmd`](./versioncmd) projects.
