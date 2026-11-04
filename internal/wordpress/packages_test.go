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

func TestDownloadReleaseChecksTheZipAndSendsTheToken(t *testing.T) {
	zip := []byte("zip bytes")
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write(zip)
	}))
	defer server.Close()

	data, err := DownloadRelease(context.Background(), server.Client(), server.URL+"/acme.zip", sum(zip), "release-token")
	if err != nil || string(data) != "zip bytes" || auth != "Bearer release-token" {
		t.Fatalf("data %q, err %v, auth %q", data, err, auth)
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
		case r.URL.Path == "/repos/acme/plugin/releases/tags/v1.0.0":
			fmt.Fprintf(w, `{"assets": [{"name": "plugin.zip", "url": "%s/assets/1"}]}`, server.URL)
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

	data, err := DownloadRelease(context.Background(), server.Client(), server.URL+"/acme/plugin/releases/download/v1.0.0/plugin.zip", sum(zip), "")
	if err != nil || string(data) != "release zip" {
		t.Fatalf("data %q, err %v", data, err)
	}
}
