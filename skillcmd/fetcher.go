// fetcher.go — the network boundary, behind an interface so every cache
// scenario unit-tests with a fake and nothing in the package needs a
// network.
package skillcmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fetcher retrieves a version's published bundle and the release's
// checksums.txt. Implementations return raw bytes; verification is the
// caller's job (one verifier, not one per fetcher).
type Fetcher interface {
	Fetch(repo, tag, version string) (bundle, checksums []byte, err error)
}

// urlFetcher downloads release assets over plain HTTPS from a URL template
// carrying {tag} and {asset} placeholders (Options.AssetURLTemplate) — the
// forge's browser-download shape, which serves public assets
// unauthenticated with no API rate limit on both GitHub and GitLab.
type urlFetcher struct {
	tpl string
}

func (f urlFetcher) url(tag, asset string) string {
	r := strings.NewReplacer("{tag}", tag, "{asset}", asset)
	return r.Replace(f.tpl)
}

func (f urlFetcher) Fetch(repo, tag, version string) ([]byte, []byte, error) {
	bundle, err := httpGet(f.url(tag, bundleAssetName(version)))
	if err != nil {
		return nil, nil, err
	}
	sums, err := httpGet(f.url(tag, "checksums.txt"))
	if err != nil {
		return nil, nil, err
	}
	return bundle, sums, nil
}

// fetchTimeout bounds one asset download. Generous — release bundles are
// hundreds of KB — but finite: a skills command must never hang a terminal
// (or an agent) on a stalled connection.
const fetchTimeout = 60 * time.Second

// fetchClient is shared across requests so connection reuse works between
// the bundle and checksums downloads of one Fetch call.
var fetchClient = &http.Client{Timeout: fetchTimeout}

func httpGet(url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: 404 — this version was published without a skills bundle (release with managed skills to attach one)", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
