# Forge provider abstraction — GitHub today, any code host later

**Status: Phase A shipped 2026-08-22** — the seam exists at `apps/volt/forge` (interface + GitHub implementation + detection with behavior-preserving default), and every previously-inline GitHub reference in command code routes through it (publish driver, repo identity, asset/changelog/tarball/clone URL shapes, auth probes, release-tag listing). Second-forge implementations remain deliberately deferred; the `forge:` config override lands with the first second forge (an override key that changes nothing would parse as valid while silently lying). Written 2026-08-22.

## The problem

volt is functionally coupled to GitHub: publishing shells out to `gh`, download URLs assume `github.com/<repo>/releases/download/...`, doctor probes `gh auth`, and generated CI is GitHub Actions. The user's direction is to grow volt into a framework-agnostic tool — GitLab, Gitea/Forgejo, Bitbucket, or a custom/self-hosted forge must be addable later without untangling orchestration code.

## The decision proposed here

**Do NOT build any second forge now. DO extract the seam now**, so forge-specific code has exactly one home and coupling can never silently spread:

1. A `forge.Forge` interface owning every forge-touching verb volt uses today. The existing `publish.Publisher` and skillcmd `Fetcher` interfaces fold into it as methods of one provider rather than two parallel abstractions.
2. `forge.GitHub` as the only implementation — behavior-identical to today, including the encoded gotchas (>3-tag pushes, `${{ }}` injection, token-pushed tags trigger nothing, anonymous API rate limits).
3. A hard rule (ADR): **nothing outside the `forge` package may name a forge.** Orchestration (`release`, `update`, `doctor`, `genfiles`, install scripts, skillcmd wiring) consumes the interface only. Violations are reviewable drift, not accidents.
4. **Capability flags, not pretense.** Forges differ in concept, not just URL shape — Bitbucket has no releases at all, GitLab needs project-ID asset URLs, only some forges have a latest-redirect. The interface declares capabilities (`HasReleases()`, `HasLatestRedirect()`), and volt degrades loudly where one is missing, matching the credential-gated-channel pattern.

## The interface (sketch)

```go
type Forge interface {
    ParseRemote(url string) (Repo, bool)          // is this origin mine? → owner/name
    CreateOrUpdateRelease(tag, title, notes string, assets []string) error
    VerifyRelease(tag string) (Release, error)     // re-read what was published
    AssetURL(repo Repo, tag, asset string) string  // update, install scripts, skillcmd fetch
    LatestURL(repo Repo, asset string) string      // "" when the forge has no redirect
    Doctor() []Check                               // auth present, scope, API reachable
    CIFiles() []genfiles.File                      // workflow stubs this forge generates
    SecretExpr(name string) string                 // ${{ secrets.X }} vs $X in CI YAML
}
```

Detection: parse the origin remote host → registry lookup; `forge:` in `.volt.yml` overrides for self-hosted domains. Unknown host with no override = hard error naming the fix, never a silent GitHub assumption.

## What was learned scoping the providers (recorded so it is not re-derived)

| Forge | Releases | Notes |
|---|---|---|
| Gitea/Forgejo | GitHub-near-identical API, Actions-compatible CI | the cheap second forge; proves N=2 |
| GitLab | releases exist; asset URLs need project IDs; CI is `.gitlab-ci.yml` (different language) | medium effort, mostly CI generation + live E2E |
| Bitbucket | **no releases** — only a per-repo Downloads section, no notes body, no latest-redirect | the concept does not map; support only behind capability flags, on real demand |

Two structural insights from the scoping:

- **Multi-forge forces dropping the `gh` CLI for native REST.** No CLI exists uniformly across forges; N CLIs = N auth stories. Native HTTP (as skillcmd's fetcher already does, httptest-testable) makes providers uniform. This is the largest single chunk and lands only with the first real second forge.
- **CI generation multiplies per forge, but volt's logic-in-binary design already minimized it.** The right shape is a published volt container image; each forge's generated CI file is a thin "run `volt ci` in that image" — idiomatic on GitLab/Bitbucket runners, and Gitea runs Actions YAML nearly verbatim. No per-forge volt-action ports.

## Why not build a second forge now

No consumer exists, and the voltkit standard is live E2E per guarantee — a second forge means a demo repo on that forge, real releases, runner CI, and cold-cache skillcmd fetches there, re-verified on every release-path change, forever. That cost is justified by a real repo needing it, not by symmetry. The seam extraction alone delivers the actual goal: proof the architecture is forge-agnostic, at ~1–2 sessions, fully covered by existing tests.

## Triggers to revisit

- First real repo on GitLab/Gitea/self-hosted → build that provider (order of cheapness: Gitea, GitLab, Bitbucket-never-until-demanded).
- Any PR that names GitHub outside the `forge` package → the ADR fires; route it through the seam instead.
