// proxy.go — the library half of publish verification: warming the Go module
// proxy (spec, "volt release" step 7).
//
// For a library the proxy entry IS the artifact: until
// proxy.golang.org resolves module@version, the release has not happened for
// consumers. Warming also pre-populates the cache, so the first real user
// does not pay the cold fetch.
package publish

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PublicProxy is the canonical public proxy. A constant, not config: private
// modules do not fail against a different proxy — they fail auth, which the
// caller reports as a warning, not an error.
const PublicProxy = "https://proxy.golang.org"

// WarmProxy asks the public proxy to resolve module@version. Returns nil on
// success and a descriptive error otherwise. Runs in a throwaway temp dir so
// no local go.mod or workspace can interfere with resolution.
func WarmProxy(module, version string) error {
	tmp, err := os.MkdirTemp("", "volt-proxy-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cmd := exec.Command("go", "list", "-m", module+"@"+version)
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(),
		"GOPROXY="+PublicProxy,
		"GONOSUMCHECK=", // let GOSUM verify normally; only the proxy is pinned
		"GOWORK=off",
		"GOFLAGS=-mod=mod",
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("proxy could not resolve %s@%s: %s", module, version, strings.TrimSpace(out.String()))
	}
	return nil
}

// ModulePath resolves the module path a released directory publishes as —
// the identity the proxy resolves, NOT derivable from the repo URL.
// `go list -m` (not reading go.mod directly) so both shapes work: a
// submodule with its own go.mod, and a directory releasing as part of a
// parent module.
func ModulePath(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-m")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go list -m in %s: %s", dir, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
