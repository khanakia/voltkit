module github.com/khanakia/voltkit/apps/demo

go 1.26.4

require (
	github.com/khanakia/voltkit/versioncmd v0.0.0
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/khanakia/voltkit/output v0.0.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/ubgo/buildinfo v0.1.2 // indirect
)

// `replace` is honoured only in the MAIN module and is NOT transitive, so flags
// and output must be listed here even though this binary reaches them only
// through version/.
replace github.com/khanakia/voltkit/versioncmd => ../../versioncmd

replace github.com/khanakia/voltkit/output => ../../output
