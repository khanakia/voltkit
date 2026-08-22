# Contributing to voltkit

How to build, test and work on this repo. Usage docs live in the [README](./README.md) and each module's own README.

## Setup

```sh
git clone https://github.com/khanakia/voltkit && cd voltkit
task ci          # the full gate: fmt-check, vet, race tests, build, smoke
```

Requires Go (version from each module's `go.mod` via `GOTOOLCHAIN=auto`), [Task](https://taskfile.dev), git, and optionally `golangci-lint` and `gh`.

## Repo layout

```
voltkit/           <- not a module; go.work workspace + Taskfile + docs
  output/   go.mod    <- kit modules: importable libraries, per-module tags (output/vX.Y.Z)
  appdir/   go.mod
  versioncmd/ go.mod
  skillcmd/ go.mod    <- the `skills` subcommand kit module (SKILL.md serving)
  apps/
    demo/   go.mod    <- the reference binary built FROM the kit (releases as `demo`)
      appmeta/        <- per-app identity, generated
    volt/   go.mod    <- the volt tool (releases as `volt`, tags volt/vX.Y.Z; owns forge/, the code-host seam)
  docs/             <- roadmap and proposals (public)
```

The root is deliberately **not** a module — `go build ./...` from here fails by design; every task iterates the modules explicitly.

## Tasks

Each module carries its own `Taskfile.yml` with the **same task names**, reachable by namespace:

```sh
task --list                   # everything
task ci                       # repo-wide gate (what CI runs)
task volt:ci                  # one module's full gate (vet + race + smoke)
task versioncmd:test:cover    # coverage for one module
task versioncmd:test:uncovered # functions below 100%
task dev -- version           # run the reference binary from source
```

## Developing the volt tool

```sh
task install     # build the WORKING TREE into $GOPATH/bin/volt
```

Dev builds are stamped `v0.0.0-dev.<sha>[.dirty]` so `volt --version` always tells you which code is running and a dev build can never be mistaken for a release. Test against the demo fixtures ([volt-demo-cli](https://github.com/khanakia/volt-demo-cli), [volt-demo-lib](https://github.com/khanakia/volt-demo-lib), [volt-demo-clis](https://github.com/khanakia/volt-demo-clis)):

```sh
volt ci
volt cover
volt release . --snapshot    # the full release pipeline, publishes nothing
```

If `volt` resolves to a Homebrew install instead (`which volt`), either put `$(go env GOPATH)/bin` earlier in `PATH` or `brew unlink volt` while developing. The brew formula serves the last *released* version by design.

**Releasing is always a deliberate, manual act** — `volt release apps/volt vX.Y.Z` run by a human, or the manual-dispatch workflow. Nothing releases on push, and nothing about development requires a release.

## Design rules

**Take the narrowest dependency you actually use.** [`versioncmd`](./versioncmd) reads one field, so it takes a `string`. [`appdir`](./appdir) reads five, so it takes an `appmeta.Meta`. Requiring a config struct for one string forces every consumer to adopt that struct.

**The library owns behaviour; the app owns identity.** No kit package hardcodes an app name — the name arrives as a parameter and is interpolated into help text at construction. A test builds each command for a foreign app and asserts none of this template's identity survives.

**stdout is data, stderr is diagnostics.** `app list --json | jq` must work with zero flags.

**Bake policy, never paths.** A database path stamped at build time either creates a directory named `~` or bakes the CI runner's home directory into every user's binary.

**Split modules on dependency weight, not tidiness.** A module boundary buys independent versioning and lets consumers skip deps they don't want; it costs a tag stream, a `replace` line in every dependent, and coordinated releases. If merging costs consumers nothing, merge.

**Command modules carry the `-cmd` suffix; library modules are plain nouns.** `versioncmd` and `skillcmd` provide cobra commands; `appdir` and `output` are libraries. The suffix is load-bearing, not style: a command module's natural noun tends to collide with a content-directory convention in consuming repos (`skills/` holds skill content, `config/` will hold config) — the suffix dodges that class permanently. Decided 2026-08-22 while `version` had no published tags, i.e. at zero cost.

**The volt tool imports nothing from the kit.** Its commands must work on any Go repository; every kit-specific value is a config default, never an import. Adding a voltkit dependency to `apps/volt` is a design regression.

## Tests

Every package carries unit tests (`task test`, race via `task test:race`). The repo gate ends with a smoke test: the volt tool builds the reference binary, verifies checksums, extracts the archive, and asserts the stamped version — the artifact is verified, not the exit code. `volt cover` reports coverage; `versioncmd/` maintains 100% of its own surface.
