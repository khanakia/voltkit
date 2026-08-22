# Roadmap

Deferred and planned work across the kit. Each line points at its proposal where one exists. (History note: earlier entries here — `volt new` scaffolding, install scripts, the release workflow — shipped inside `apps/volt` and left the roadmap.)

## Planned

| Item | Notes |
|---|---|
| `dbent` module (re-introduction) | ent schema + pure-Go SQLite plumbing. A skeleton existed and was removed 2026-08-22 pending a real consumer; returns when one exists — pragma block, `QuickCheck`, `WithTx`, codegen |
| `errcodes` module | stable error-code registry: `CLIError` with code, hint, and doc URL; `app errors list --json` as living documentation |
| `config` module | configuration precedence chain: flag → env → project file → user file → default, resolved over `appdir` categories; owns typed binding |
| `doctorcmd` module | environment health check: reports every `appdir` category and the rung that produced it |
| second forge live support | GitLab is built behind the forge seam but unsupported until its live E2E runs — see `docs/proposals/forge-provider.md` |
| providers for web / api | `volt ci` / `build` beyond Go repos (ADR-R12 provider interface exists; Go is the only implementation) |

## Done and moved out

`volt new` + templates, install scripts, the manual-dispatch release workflow, coverage badges, hooks, skills serving (`skillcmd`), and the forge seam all live in the shipped tool and kit — see the root README and `apps/volt/README.md`.
