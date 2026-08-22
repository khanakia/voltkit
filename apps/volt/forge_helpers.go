// forge_helpers.go — the one place commands resolve their forge: the repo
// root's `forge:` override (self-hosted hosts) composed with detection.
package main

import (
	"strings"

	"github.com/khanakia/voltkit/apps/volt/forge"
	"github.com/khanakia/voltkit/apps/volt/genfiles"
	"github.com/khanakia/voltkit/apps/volt/voltcfg"
)

// detectForge resolves the forge for the repo at dir, honoring a `forge:`
// override in the ROOT .volt.yml (the forge is a repo-level fact — per-dir
// overrides would let one repo publish to two hosts, which no tag grammar
// could express).
func detectForge(dir string) (forge.Forge, error) {
	override := ""
	if cfg, err := voltcfg.Load(dir); err == nil {
		override = cfg.Forge
	}
	return forge.Detect(dir, override)
}

// forgeVars renders the forge's URL shapes into the template variables the
// shared install scripts consume (FG-D4). The ${VERSION} / $Version
// literals are expanded by the SCRIPTS at run time — each script's own
// variable syntax, hence two download bases.
func forgeVars(f forge.Forge, repo forge.Repo, binary string) genfiles.Vars {
	trim := func(s string) string { return strings.TrimSuffix(s, "/") }
	latest := ""
	if u := f.LatestAssetURL(repo, ""); u != "" {
		latest = trim(u)
	}
	return genfiles.Vars{
		Repo:           repo.String(),
		Binary:         binary,
		Version:        voltVersion,
		DownloadBase:   trim(f.AssetURL(repo, "${VERSION}", "")),
		DownloadBasePS: trim(f.AssetURL(repo, "$Version", "")),
		LatestBase:     latest,
		RawScriptURL:   f.RawFileURL(repo, "install.sh"),
		RawScriptURLPS: f.RawFileURL(repo, "install.ps1"),
	}
}
