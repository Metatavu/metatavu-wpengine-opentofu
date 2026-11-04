package wordpress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func TestDownloadReleaseChecksTheZipAndSendsTheTokenOnlyToGitHub(t *testing.T) {
	zip := []byte("zip bytes")
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write(zip)
	}))
	defer server.Close()

	// Any other host, and plain HTTP, never gets the GitHub token.
	data, err := DownloadRelease(context.Background(), server.Client(), server.URL+"/acme.zip", sum(zip), "release-token")
	if err != nil || string(data) != "zip bytes" || auth != "" {
		t.Fatalf("data %q, err %v, auth %q", data, err, auth)
	}

	tokenOrigins[server.URL] = true
	defer delete(tokenOrigins, server.URL)
	if _, err := DownloadRelease(context.Background(), server.Client(), server.URL+"/acme.zip", sum(zip), "release-token"); err != nil || auth != "Bearer release-token" {
		t.Fatalf("err %v, auth %q", err, auth)
	}
	if _, err := DownloadRelease(context.Background(), server.Client(), server.URL+"/acme.zip", sum([]byte("other")), ""); err == nil || !strings.Contains(err.Error(), "not the declared") {
		t.Fatalf("err %v", err)
	}
}

// When github.com doesn't serve a release asset (it answered 500 for one in
// 2026-11), the same asset comes through the API.
func TestDownloadReleaseFallsBackToGitHubsAPI(t *testing.T) {
	zip := []byte("release zip")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/acme/plugin/releases/download/"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.EscapedPath() == "/repos/acme/plugin/releases/tags/plugin%2Fv1.0.0":
			fmt.Fprintf(w, `{"assets": [{"name": "plugin 1.zip", "url": "%s/assets/1"}]}`, server.URL)
		case r.URL.Path == "/assets/1" && r.Header.Get("Accept") == "application/octet-stream":
			w.Write(zip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	githubRelease, githubAPI = regexp.MustCompile(`^`+regexp.QuoteMeta(server.URL)+`/([^/]+)/([^/]+)/releases/download/([^/]+)/([^/?#]+)$`), server.URL
	defer func() {
		githubRelease = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/releases/download/([^/]+)/([^/?#]+)$`)
		githubAPI = "https://api.github.com"
	}()

	// A tag with a slash and an asset name with a space, escaped in the URL as GitHub does.
	data, err := DownloadRelease(context.Background(), server.Client(), server.URL+"/acme/plugin/releases/download/plugin%2Fv1.0.0/plugin%201.zip", sum(zip), "")
	if err != nil || string(data) != "release zip" {
		t.Fatalf("data %q, err %v", data, err)
	}
}
