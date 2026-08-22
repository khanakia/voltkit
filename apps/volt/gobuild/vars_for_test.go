package gobuild

import (
	"testing"

	"github.com/khanakia/voltkit/apps/volt/buildmeta"
)

// vars builds a fixed Vars for deterministic ldflags tests.
func vars(t *testing.T) buildmeta.Vars {
	t.Helper()
	return buildmeta.Vars{Version: "v0.1.0", Commit: "c", ShortCommit: "c", BuildTime: "t"}
}
