// commands.go — the app's command registry.
//
// Behaviour lives in the kit's command modules; this file only declares WHICH commands
// this binary exposes and supplies the app-specific data they need. Keeping the
// wiring in one place is what makes the removal recipes in
// docsi/ARCHITECTURE.md a three-line edit.
package main

import (
	"github.com/khanakia/voltkit/apps/demo/appmeta"
	"github.com/khanakia/voltkit/versioncmd"

	"github.com/spf13/cobra"
)

// components are the contract surfaces this app versions independently of its
// binary version. Scaffolding a new project edits this list.
//
// Declared here rather than in the kit because only the app knows what it
// versions; the command carries whatever it is handed.
func components() []versioncmd.Component {
	return []versioncmd.Component{
		{Name: "db_schema", Version: 1},
		{Name: "config_schema", Version: 1},
	}
}

// registerCommands attaches every subcommand to root.
func registerCommands(root *cobra.Command, meta appmeta.Meta) {
	root.AddCommand(versioncmd.New(
		// The command asks for a name, not for our config type — so the
		// adaptation is one field access here rather than a dependency there.
		versioncmd.WithBinaryName(meta.Binary),
		versioncmd.WithComponents(components()...),
		versioncmd.WithAliases("v"),
	))
}
