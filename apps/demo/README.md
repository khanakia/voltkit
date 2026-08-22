# apps/demo

The **reference binary** assembled from the kit — the living proof the modules compose, and the fixture `task ci`'s smoke test builds with volt itself. It is deliberately thin: everything it does is wiring; behaviour lives in the kit modules. (The family *tool* is its sibling, [`apps/volt`](../volt) — this app only demonstrates the kit.)

```sh
task build && ./bin/demo version --json
```

## What the wiring looks like

```
apps/demo/
  main.go       root command, signal-cancellable context, error rendering
  commands.go   the command registry — WHICH commands exist, and their app-specific data
  appmeta/      this app's identity: name, env prefix, directory names, DB policy
```

`appmeta` lives here rather than in the kit because it is generated per-app — every project built on the kit has its own. No kit module imports it; commands take the one value they need (`versioncmd` takes a name, `appdir` takes an app key), so identity flows in as a parameter rather than a dependency.

## Adding a command

One line, in one file:

```go
// apps/demo/commands.go
func registerCommands(root *cobra.Command, meta appmeta.Meta) {
	root.AddCommand(versioncmd.New(
		versioncmd.WithBinaryName(meta.Binary),
		versioncmd.WithComponents(components()...),
		versioncmd.WithAliases("v"),
	))
}
```

## No local version variable

The binary carries no `var version` of its own. Provenance is resolved by the [`versioncmd`](../../versioncmd) module, which reads the `-ldflags` stamp when the release pipeline set one and otherwise falls back to the module version the Go toolchain records for `go install`. A local `var version = "dev"` would shadow that and silently report `dev` on installed builds.

## Renaming this into your own project

Prefer `volt new cli <name>` — it scaffolds this shape with your identity filled in. Doing it by hand, `appmeta.Default` is the single source of identity; change it and every string follows (help text, state directory, env prefix):

```go
var Default = appmeta.Meta{
	Name: "acme", Binary: "acme", EnvPrefix: "ACME",
	DirName: ".acme", DBFilename: "acme.db",
	DBLocation: appmeta.LocationProjectLocal,
}
```

## Release identity

This app releases under its directory name — tag `demo/vX.Y.Z` — so the family tool at `apps/volt` uniquely owns the release name `volt` (see `.volt.yml`).
