# appdir

Decides where an application's state lives on disk — and reports **how** it decided.

```go
import "github.com/khanakia/voltkit/appdir"
```

## Why this exists

"Why is my data missing?" is the most common support question a local-first CLI gets, and it is almost always this package's answer differing from what the user assumed. So resolution is explicit and reportable: `Resolve` returns not just a path but the `Rung` that produced it.

## Usage

```go
d, err := appdir.New("volt")          // DirName -> .volt, EnvPrefix -> VOLT
if err != nil {
	return err
}

dbPath, err := d.DataFile("volt.db")
if err != nil {
	return err
}
if err := appdir.VerifyNotSymlink(dbPath); err != nil {
	return err
}
if err := d.Ensure(appdir.Data); err != nil {
	return err
}

db, err := sql.Open("sqlite3", dbPath)
```

It takes an app key and plain options, never the caller's config struct — a framework module that demands its consumer's identity type inverts the dependency. An app holding one adapts at the call site:

```go
d, err := appdir.New(meta.Name,
	appdir.WithDirName(meta.DirName),
	appdir.WithEnvPrefix(meta.EnvPrefix),
)
```

Print the rung in `doctor` — it turns a support conversation into one line of output:

```go
rung, _ := d.Rung(appdir.Data)
fmt.Printf("database: %s (resolved via %s)\n", dbPath, rung)
// database: /repo/.volt/data/volt.db (resolved via project-local)
```

## Two axes

A path is the product of two independent choices.

**Category** — what kind of file, grouped by lifecycle:

| Category | Holds | Lifecycle |
|---|---|---|
| `Config` | settings a human edits | precious, may be committed |
| `Data` | database, user content | precious, never committed |
| `Cache` | downloads, derived artifacts | **disposable**, must never be backed up |
| `State` | logs, history | disposable |

**Strategy** — where that category lives:

| Strategy | Resolves to |
|---|---|
| `ProjectLocal` | nearest `.volt/` walking up — the git model |
| `HomeDotfile` | `~/.volt/` — one path on every OS, the `~/.aws` style |
| `OSNative` | the platform's conventional directory |
| `CustomPath` | an absolute path you supply |

Any category may use any strategy. That product is the point:

```go
d, _ := appdir.New("volt", appdir.WithLayout(appdir.Layout{
	appdir.Config: appdir.ProjectLocal,
	appdir.Data:   appdir.ProjectLocal,
	appdir.Cache:  appdir.OSNative,   // machine-wide, shared, disposable
}))
```

A layout need not be exhaustive — omitted categories fall back to `DefaultLayout`.

## Default layout

| Category | Strategy | Why |
|---|---|---|
| config | `ProjectLocal` | settings describe this project |
| data | `ProjectLocal` | the database belongs to this project |
| cache | `OSNative` | machine-wide and shared; the OS already excludes it from backups |
| state | `OSNative` | logs are machine-scoped |

A per-project cache means ten projects carrying ten copies of identical downloads, and `cache clear` hunting through every checkout. Every serious tool shares one — `~/.npm`, `~/.cargo/registry`, Go's build cache.

Presets: `DefaultLayout`, `AllProjectLocal`, `AllHomeDotfile`, `AllOSNative`.

## OS-native mapping

| | Linux | macOS | Windows |
|---|---|---|---|
| config | `$XDG_CONFIG_HOME` or `~/.config/volt` | `~/Library/Application Support/volt` | `%AppData%\volt` |
| data | `$XDG_DATA_HOME` or `~/.local/share/volt` | `~/Library/Application Support/volt` | `%LocalAppData%\volt` |
| cache | `$XDG_CACHE_HOME` or `~/.cache/volt` | `~/Library/Caches/volt` | `%LocalAppData%\volt\Cache` |
| state | `$XDG_STATE_HOME` or `~/.local/state/volt` | `~/Library/Logs/volt` | `%LocalAppData%\volt\State` |

Windows splits on the roaming boundary: config roams with the user across machines, data and cache do not — roaming a database would copy it over the network on every login.

```go
appdir.Platform()   // "linux" | "darwin" | "windows"
```

## Project-local layout and .gitignore

Config sits at the root of the project directory; every other category gets a subdirectory. That is deliberate — it lets a team commit shared config without committing the database:

```
.volt/
  config.json     <- commit this
  data/           <- gitignore
  cache/          <- gitignore
  state/          <- gitignore
```

```gitignore
.volt/data/
.volt/cache/
.volt/state/
```

## Precedence

Per category, first hit wins:

```
WithCustom  ->  $VOLT_<CATEGORY>_DIR  ->  $VOLT_HOME  ->  layout strategy
```

`VOLT_HOME` is the blunt escape hatch: set it and every category becomes a subdirectory of that one path, overriding the layout. Same idea as `CARGO_HOME`.

A set-but-empty variable is **not** an override — honouring `VOLT_CACHE_DIR=` would resolve the cache to the current working directory.

## Fallback: data fails, config falls back

With a project-local category and no project directory found, behaviour depends on the category:

```go
_, err := d.DataFile("volt.db")
errors.Is(err, appdir.ErrNoProjectDir)   // true — data does NOT fall back
```

**`Data` fails.** Silently resolving elsewhere opens a second, empty database, and the user reads that as data loss. The error names the directory it looked for.

**`Config`, `Cache`, `State` fall back** to `HomeDotfile`, then `OSNative`. User-level defaults applying outside a project is normal — exactly how git treats `~/.gitconfig`.

```go
fellBack, _ := d.FellBack(appdir.Config)   // true, so doctor can say so
```

## Security

`VerifyNotSymlink` refuses a database path that is a symlink:

```go
if err := appdir.VerifyNotSymlink(r.DBPath); err != nil {
	return err // "refusing to use ...: it is a symlink"
}
```

Anyone able to write into the state directory could otherwise replace the database with a link to a file elsewhere, and the next write would clobber that target. This uses `os.Lstat` — `os.Stat` follows the link and reports the victim, so it structurally cannot detect this.

`EnsureDir` creates directories with `DirPerm` (`0700`), not `0755`: the database holds whatever the user chose to store, and on a shared machine a world-readable state directory leaks it. Existing directories are left alone rather than chmod'ed — a user who deliberately widened permissions should not have that silently reverted.

## Testing

Every external input is injectable, so no test mutates process state — a test that chdirs or sets an environment variable breaks its neighbours under `-race`:

```go
d, _ := appdir.New("volt",
	appdir.WithWorkingDir(tmp),
	appdir.WithHomeDir(func() (string, error) { return fakeHome, nil }),
	appdir.WithEnvLookup(func(string) (string, bool) { return "", false }),
	appdir.WithPlatform(appdir.Windows),   // exercise any OS row from any host
)
```

`WithPlatform` is what makes the OS-native table testable at all: the Windows and Linux rows otherwise never execute on a macOS machine, and two thirds of the mapping would ship unverified.

Pass `WithEnvLookup` explicitly even when expecting nothing — otherwise a stray `VOLT_CACHE_DIR` in a developer's shell silently changes the result.

The package ships at **100% statement coverage**. It is pure path arithmetic over injectable inputs, so an untested branch has no excuse — and a mis-resolved path is what users experience as data loss.

## API

| Symbol | Purpose |
|---|---|
| `New(appKey, ...Option) (*Dirs, error)` | resolve every category |
| `d.ConfigFile` / `DataFile` / `CacheDir` / `StateDir` | a path inside that category |
| `d.Path(cat, elem...)` | the general form |
| `d.Dir(cat)` / `d.Rung(cat)` / `d.FellBack(cat)` | the directory and how it was chosen |
| `d.Ensure(cat)` / `d.EnsureAll()` | mkdir at `0700` |
| `VerifyNotSymlink(path)` | refuse a symlinked path |
| `Platform()` / `CurrentPlatform()` | which OS mapping applies |
| `WithLayout` / `WithCustom` / `WithDirName` / `WithEnvPrefix` | configuration |
| `WithPlatform` / `WithEnvLookup` / `WithHomeDir` / `WithWorkingDir` | seams |

Accessors are pure path arithmetic and never touch the filesystem. Creation is always explicit via `Ensure`, because whether a missing directory is an error (most commands) or something to create (`init`) is the caller's decision.

Errors are deferred to the accessor rather than returned from `New`: a command needing only the cache must still run outside a project, even though `Data` cannot resolve there.
