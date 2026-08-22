// skillcmd — a `skills` subcommand any CLI can mount: serves the project's
// published SKILL.md agent skills, always matched to the binary's version.
// Design: docsi/SKILLCMD_SPEC.md. Deliberately dependency-light: cobra only.
module github.com/khanakia/voltkit/skillcmd

go 1.26.4

require github.com/spf13/cobra v1.10.2

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)
