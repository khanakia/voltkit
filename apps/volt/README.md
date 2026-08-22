# volt — the family tool

The `volt` binary that builds, releases and ships Volt-family projects — and, by design, any Go repository at all. Design spec: [`docsi/RELEASE_PIPELINE_SPEC.md`](../../docsi/RELEASE_PIPELINE_SPEC.md); this README covers what is **built today**.

```sh
volt new cli mytool                      # scaffold from khanakia/volt-cli templates (--template cobra, --list)
volt status                              # per-stream: last tag, commits since, suggested next version
volt ci                                  # the gate: fmt, vet, golangci, race tests — changed modules
volt ci --fix                            # apply gofmt/lint auto-fixes, report what remains
volt build ./apps/demo --version v1.4.0   # cross-compile + archive + checksums into dist/
volt release ./cmd/notes v1.4.0          # tests → tag → build → publish → verify
volt release ./cmd/notes --bump patch    # same, next version computed from the stream
volt release --from-tag notes/v1.4.0     # (re)publish an existing tag — CI / recovery path
                                         # NOTE: pushing a tag releases nothing (ADR-R14) —
                                         # releases are the command above, or Run workflow with a tag
volt release ./cmd/notes --snapshot      # build everything, publish nothing
volt gen                                 # workflow stubs + install scripts, hash-guarded
volt gen skills                          # skillcmd wiring + one-time starter skill (see voltkit/skillcmd)
volt doctor                              # is this repo releasable? (tools, auth, remote, pins)
volt cover --badge coverage.svg          # coverage + self-contained README badge (--check for CI)
volt update                              # self-update from volt's own releases
volt upgrade --ref v0.2.0                # re-apply template evolution — three-way, edits survive
```

## What `volt build` does

Given a directory holding `package main`: cross-compiles it for every configured platform (default darwin/linux × amd64/arm64 + windows/amd64, `CGO_ENABLED=0`), stamps the version via ldflags, archives each binary (tar.gz; zip on windows), and writes a `sha256sum -c`-compatible `checksums.txt`. No network, no tokens, no git writes — safe to run anywhere, identical locally and in CI.

After every build it **verifies the stamp landed** by searching the binary for the rendered value — Go silently ignores `-X` for a symbol that does not exist, and `-s` (the strip default) removes the symbol table, so neither a green build nor `go tool nm` proves anything.

## Configuration

None required. An optional `.volt.yml` beside the built directory overrides what cannot be detected. The full annotated schema:

```yaml
# .volt.yml — every field optional; a missing file means all defaults.
binary: mycli                        # default: the directory name; also the CLI tag prefix (mycli/vX.Y.Z)
platforms: [darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, windows/amd64]
extra_files: [README.md, LICENSE]    # shipped inside each release archive

ldflags:
  strip: true                        # -s -w (default true; set false for debug symbols)
  vars:                              # -X targets; a non-empty map REPLACES the default
    main.version: "{{.Version}}"     #   ubgo/buildinfo stamp map wholesale
    main.commit: "{{.ShortCommit}}"
  extra: ["-X main.channel=stable"]  # raw passthrough — the escape hatch

cgo: false                           # true needs a toolchain — see the spec's cgo section
toolchain:
  cc: "zig cc -target {{.ZigTarget}}"

brew:                                # Homebrew channel; omit to disable (skips loudly)
  tap: khanakia/homebrew-tap
  description: "One-line formula description"
  license: MIT

internal: true                       # never released: hidden from status, refused by
                                     # release, invisible to tag resolution; applies to
                                     # this directory AND everything under it

hooks:                               # project-owned scripts around the release
  pre_release: ./scripts/gate.sh     #   your own gate — runs before the tag; failure
                                     #   aborts with nothing created
  post_release: ./scripts/promote.sh #   after publish+verify — promote to a public
                                     #   repo, notify, mirror; failure cannot un-release.
                                     #   Repo-root cwd; context via VOLT_TAG, VOLT_VERSION,
                                     #   VOLT_DIR, VOLT_BINARY, VOLT_KIND, VOLT_DIST.
                                     #   --snapshot runs neither; --from-tag runs post only

forge: gitlab                        # ONLY for self-hosted hosts detection cannot classify;
                                     #   gitlab.com / github.com detect from the origin remote
                                     #   with zero config. Repo ROOT .volt.yml only.

generated:                           # written by `volt new` — the upgrade stamp; opaque
  ...                                # to build/release, do not edit by hand
```

## Release hooks — where each hook's output appears

Hooks run inside `volt release`, so their output lands wherever that command runs — which differs per hook because of the `--from-tag` rules:

| Hook | Where you see its output |
|---|---|
| `pre_release` | Only in the terminal of whoever runs `volt release <dir> <version>` — never in CI, because CI's release workflow runs `--from-tag`, which skips the gate by design (the tag exists; there is nothing left to prevent) |
| `post_release` | That same terminal, **and** the release-workflow Actions log when a tag is (re)published via Run workflow — the volt-action step prints `hook post_release: <script>` followed by the script's own output |

A `ci` workflow run never shows hook output: `volt ci` does not run hooks — they are release-time only.

### A worked example — the demo gate and announcer

[volt-demo-clis](https://github.com/khanakia/volt-demo-clis) wires both hooks on its `notes` stream. The gate is any executable that exits non-zero to refuse; this one refuses when an env var says releases are frozen:

```sh
#!/bin/sh
# scripts/release-gate.sh — pre_release: runs after tests, BEFORE the tag.
# Non-zero exit aborts the release with nothing created.
if [ "$RELEASE_BLOCKED" = "1" ]; then
  echo "gate: releases are blocked (RELEASE_BLOCKED=1) — refusing $VOLT_TAG" >&2
  exit 1
fi
echo "gate: ok — $VOLT_TAG ($VOLT_KIND at $VOLT_DIR)"
```

```sh
#!/bin/sh
# scripts/announce.sh — post_release: runs after publish+verify.
# Write OUTSIDE the repo: dirtying the working tree breaks --from-tag republishes.
LOG="${ANNOUNCE_LOG:-/tmp/volt-announce.log}"
echo "released $VOLT_TAG version=$VOLT_VERSION binary=$VOLT_BINARY dist=$VOLT_DIST" >> "$LOG"
```

Hooks inherit the caller's environment, so rehearsing the refusal path is one prefix — no config change, no commit:

```sh
$ RELEASE_BLOCKED=1 volt release ./cmd/notes --bump patch
bump patch: next version is v0.2.1
testing cmd/notes
hook pre_release: ./scripts/release-gate.sh
gate: releases are blocked (RELEASE_BLOCKED=1) — refusing notes/v0.2.1
Error: pre_release hook refused the release — nothing was tagged
$ git tag -l 'notes/v0.2.1'        # nothing — locally or on the remote
```

(`RELEASE_BLOCKED` is the demo script's own convention, not a volt flag — volt only runs the script and honors its exit code. Your gate can check anything: a freeze file, a calendar API, CI status.)

And the successful path, as it appears in a release-workflow Actions log ([live run](https://github.com/khanakia/volt-demo-clis/actions/runs/32577876195)) — `pre_release` absent because `--from-tag` skips it:

```
released notes/v0.2.1 — 6 asset(s), verified
hook post_release: ./scripts/announce.sh
announce: logged notes/v0.2.1 to /tmp/volt-announce.log
```

**Sequencing rule for new `.volt.yml` keys:** volt rejects unknown config keys loudly (typo protection), which makes config forward-incompatible on purpose — an older volt refuses a newer repo's `.volt.yml` outright. So a feature adding a config key ships in this order: release the volt that understands the key → bump volt-action's default `version` (move its `v1` tag) → only then commit the key to any repo's `.volt.yml`. Committing the key first breaks that repo's CI releases until the new volt reaches the runners (seen live: `hooks:` committed while runners still installed volt/v0.4.0 → `field hooks not found in type voltcfg.Config`).

## `volt upgrade` — template evolution, edits survive

A project scaffolded by `volt new` can take later template improvements without losing its own changes:

```sh
volt upgrade --ref v0.2.0
# updated      .editorconfig      ← new template file created
# updated      .volt.yml          ← generation stamp advanced
# updated      README.md          ← template change AND your edits, merged
# upgraded to v0.2.0 (6f2b6df) — review with git diff, then commit
```

The mechanic (the cruft / copier-update model): re-render the template at the **recorded commit** with the **recorded inputs** from `.volt.yml`'s `generated:` stamp — that is the old state, byte-identical to what `volt new` originally wrote — render the target ref the same way, and apply the difference to your project **three-way**.

### Design notes worth knowing

- **The merge engine is `git merge-file`, never a hand-rolled diff3.** A merge engine that silently corrupts is the worst failure this feature could have; git's is twenty years hardened, its conflict markers are ones every developer already knows how to resolve, and git was a hard dependency anyway.
- **"Old" means the recorded *commit*, never the recorded ref.** Tags can move after you scaffold; if the old side were re-fetched by ref, a moved tag would silently change the computed diff for every previously scaffolded project. `scaffold` passes 40-hex SHAs through unresolved for exactly this reason.

### Guarantees

| Behaviour | Rule |
|---|---|
| Clean tree required | the upgrade is one reviewable `git diff`, revertible with `git checkout` |
| Your edits are never eaten — either direction | template deletes a file you edited → yours is kept; **you** deleted a file the template changed → stays deleted (a deletion is an edit) |
| No common ancestor → conflict, never a silent pick | a file both you and the template created surfaces both versions in markers |
| Idempotent | a second run reports `already at <ref>` and changes nothing |
| Conflicts exit non-zero | `N file(s) have conflict markers — resolve them, then commit`; scripts cannot mistake a half-merged tree for success |

## Packages

| Package | Owns |
|---|---|
| [`platform`](./platform) | GOOS/GOARCH targets, asset-name contract, zig triples |
| [`buildmeta`](./buildmeta) | template variables (`{{.Version}}` …) resolved from git, strict rendering |
| [`voltcfg`](./voltcfg) | `.volt.yml` loading — defaults with overrides, typos rejected loudly |
| [`gobuild`](./gobuild) | the build orchestrator: matrix, ldflags, stamp verification |
| [`archive`](./archive) | tar.gz / zip / `checksums.txt`, pure stdlib, deterministic |
| [`detect`](./detect) | CLI vs library via `go list` (ADR-R06: detected, never configured) |
| [`gitx`](./gitx) | git operations: dirty check, tag lifecycle, race-detecting push, ≤3-tag batching constant |
| [`relname`](./relname) | rule one: tag composition + manifest-free tag→directory resolution |
| [`changelog`](./changelog) | `## [x.y.z]` section extraction with a generated fallback |
| [`forge`](./forge) | THE seam to any code host: detection, repo identity, URL shapes, publish drivers, auth probes, per-forge CI file sets. Implementations: GitHub (gh), GitLab (glab — live E2E pending). Nothing outside it may name a forge (docs/proposals/forge-provider.md, docsi/FORGE_GITLAB_PLAN.md) |
| [`publish`](./publish) | the `Publisher` interface, gh implementation, publish verification |
| [`release`](./release) | the orchestrator: tests → reserve → build → publish → verify (ADR-R10) |
| [`cicheck`](./cicheck) | module discovery, changed-vs-base narrowing, the per-module gate |
| [`genfiles`](./genfiles) | hash-guarded generated files (ADR-R11), their embedded templates, the skills lint + `gen skills` targets |
| [`streams`](./streams) | stream discovery for `status`/`--bump`: prefixes, newest versions, commits-since |
| [`scaffold`](./scaffold) | `volt new`: pinned-ref template fetch, `_base`+variant overlay, generation stamp |
| [`covercheck`](./covercheck) | `volt cover`: per-module profiles, statement-weighted merge, SVG badge |
| [`upgrade`](./upgrade) | `volt upgrade`: stamp parsing, old/new re-render, three-way merge via `git merge-file` |

This module deliberately imports **nothing from voltkit** (ADR-R08) — every kit-specific value is a config default. Unknown subcommands exec `volt-<name>` from PATH (the kubectl/git plugin model, ADR-R12), wired in before any plugin exists because retrofitting dispatch changes argument parsing.

## Testing

Three levels; the first two run in `task ci` at the repo root.

1. **Unit tests** (`task volt:test`) — pin the public contracts: asset-name format, checksums format and determinism, ldflags ordering, the missing-stamp warning, `--native-only` skip reporting, config-typo rejection, kind-detection hard errors. Race-clean.
2. **Smoke** (`task volt:smoke`) — builds this tool, uses it to build `apps/demo` for the host platform, verifies `checksums.txt` with `shasum -c`, extracts the archive, and asserts the binary reports the stamped version. The artifact is verified, not the exit code. Assumes `tar` + `shasum` (macOS/Linux); zip is covered by unit tests.
3. **Self-hosting** — volt releasing volt: live since `volt/v0.1.0` (2026-08-21), and every volt release since has been cut by the previous one.

## Credentials — how volt authenticates, and testing with `GH_TOKEN`

Volt never stores or configures tokens. Publishing and probing go through the `gh` CLI, which resolves its credential in this order:

```
GH_TOKEN  →  GITHUB_TOKEN  →  your keychain login (gh auth login)
```

**Locally** that means zero setup: your `gh auth` identity is used, and `volt doctor` / `volt release` act as you. **In CI** the generated workflow surfaces a token as `GH_TOKEN` (the automatic `github.token` by default), and `gh` picks it up — volt needs no plumbing of its own.

### Rehearsing CI behaviour locally

Because `GH_TOKEN` outranks your keychain, setting it for one command **fully replaces your identity** — exactly what a runner experiences:

```sh
GH_TOKEN=github_pat_XXXX volt doctor        # behave as the CI token would
GH_TOKEN=github_pat_XXXX volt release . --snapshot
GH_TOKEN=garbage         volt doctor        # rehearse the expired/revoked-token path
gh auth status                              # shows WHICH credential is currently winning
gh auth token                               # prints your keychain token if you need it explicitly
```

Use cases, with how to build each token (github.com → Settings → Developer settings → Fine-grained tokens):

| To rehearse… | Token to use | What you should see |
|---|---|---|
| the healthy CI setup | fine-grained PAT, *Contents: read+write* on the target repo | doctor green |
| an expired/revoked secret | any invalid string | auth failure, clearly named |
| a token that cannot SEE a private repo | PAT scoped to no repositories | 404 (private repos 404 rather than 403) |
| read-only access | PAT with *Contents: read* only | visible but unwritable |

One boundary to know: `GH_TOKEN` governs only the **gh side** — publishing, release verification, doctor's checks. Pushing tags uses git's own credentials (SSH key / credential helper) and ignores `GH_TOKEN` entirely.

## Skills integration

Projects with a managed `skills/` directory get three behaviours: `volt gen skills` writes the [`voltkit/skillcmd`](../../skillcmd) wiring (plus a one-time starter skill), `volt ci` lints the SKILL.md frontmatter contract (presence-gated; `skills.managed: false` in `.volt.yml` opts a directory out of both), and CLI releases attach `skills_<version>.tar.gz` — the bundle the served `skills` command fetches, per binary version. Harness installation is never volt's job: that is `npx skills add <owner>/<repo>` (skills.sh).

## Status

Self-hosting since 2026-08-21: `volt/v0.1.0` was released by volt itself, and [`volt-action@v1`](https://github.com/khanakia/volt-action) runs `volt ci` / `volt release --from-tag` green on real runners. Install: `brew install khanakia/tap/volt`, or a release archive.

## Developing volt

The dev loop (`task install`, dev-version stamping, brew shadowing, the releases-are-always-manual rule) lives in the repo's [CONTRIBUTING.md](../../CONTRIBUTING.md).
