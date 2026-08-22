# skillcmd

A `skills` subcommand for any Go CLI: serves the project's published SKILL.md agent skills, **always matched to the binary's version**. The binary fetches its own version's skills bundle (attached to its release by `volt release`) into the OS cache on first use and serves from there — staleness against the binary is impossible by construction, and single-binary distribution (brew, curl, `go install`) keeps working because nothing ships beside the executable.

```go
root.AddCommand(skillcmd.New(skillcmd.Options{
	Binary:  "mycli",
	Repo:    "khanakia/mycli",   // where release bundles live
	Version: version,            // the stamped version; "dev" serves the live skills/ dir
	Tag:     "mycli/" + version, // the release tag holding the bundle (bare version for root repos)
}))
```

Wiring is generated — run `volt gen skills` and it writes `skills_gen.go` (hash-guarded) with these values filled in from your repo, including the detected forge's download URL shape (`AssetURLTemplate`), plus a one-time starter skill when `skills/` does not exist yet.

## Commands served

| Command | Does |
|---|---|
| `skills list` | every skill this exact binary version ships |
| `skills get <name>` | print one skill (or several; `--json`, `--full`) |
| `skills path [name]` | the on-disk location being served |
| `skills check <installed-dir>` | is an installed copy current for THIS binary? current → exit 0, stale → exit 1; junk like `.DS_Store` ignored |
| `skills version` | binary version, canonical skills hash, and the serving source |
| `skills refresh` | drop and re-fetch this version's cache |

## How content resolves

Three rungs, first hit wins — and every command reports which one served:

1. **Env override** (`<BINARY>_SKILLS_DIR`) — must exist, never silently ignored
2. **Live directory** (dev builds only): the nearest `skills/` walking up from cwd — editing skills needs no re-fetch
3. **Version-keyed cache**: `os.UserCacheDir()/<binary>/skills/<version>/`, fetched once per version (checksum-verified against the release's `checksums.txt`, `.complete` marker guards partial extracts, other versions cleaned)

## What this module refuses to do

**No install command.** Installing skills into an agent harness is [skills.sh](https://skills.sh)'s job: `npx skills add <owner>/<repo>`. The binary is the *source of truth and freshness* (`skills check`), never the installer — one owner per concern.

## Options

`Fetcher`, `CacheRoot`, `WorkDir` exist for tests (no network, no real cache dir needed). `AssetURLTemplate` carries the forge's download shape with `{tag}`/`{asset}` placeholders; empty defaults to the GitHub shape derived from `Repo`, so wirings generated before forges existed keep working.
