// The volt family tool: new / ci / build / release / deploy.
//
// DELIBERATELY imports nothing from voltkit (ADR-R08 in
// docsi/RELEASE_PIPELINE_SPEC.md): these commands must work on any Go
// repository, so every kit-specific value is a config default, never an
// import. Adding a voltkit dependency here is a design regression, not a
// convenience.
module github.com/khanakia/voltkit/apps/volt

go 1.26.4

require (
	github.com/spf13/cobra v1.10.2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)
