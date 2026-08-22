package skillcmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The real fetcher, exercised against a local httptest server — the 404
// message must explain the missing-bundle cause, not just the status.
func TestGithubFetcherPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "skills_v1.0.0.tar.gz"):
			_, _ = w.Write([]byte("bundle-bytes"))
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			_, _ = w.Write([]byte("aaa  skills_v1.0.0.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ok, err := httpGet(srv.URL + "/releases/download/v1.0.0/skills_v1.0.0.tar.gz")
	if err != nil || string(ok) != "bundle-bytes" {
		t.Fatalf("ok path: %q %v", ok, err)
	}
	_, err = httpGet(srv.URL + "/releases/download/v9.9.9/skills_v9.9.9.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "published without a skills bundle") {
		t.Fatalf("404 must explain the missing-bundle cause: %v", err)
	}

	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()
	_, err = httpGet(srv500.URL + "/x")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("non-404 failures must name the status: %v", err)
	}
}
