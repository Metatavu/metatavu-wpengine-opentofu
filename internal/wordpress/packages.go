package wordpress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

// Package kinds: the plugin's /plugins and /themes.
const (
	Plugins = "plugins"
	Themes  = "themes"
)

// PackageState is a plugin's or theme's state after a write.
type PackageState struct {
	Installed bool     `json:"installed"`
	Version   *string  `json:"version"`
	Active    bool     `json:"active"`
	Pending   *Pending `json:"pending"`
}

// AdoptFromWordPressOrg takes over a whitelisted package from wordpress.org
// that the project's bootstrap/ has installed (WP Engine keeps it up to
// date), and sets whether a plugin is active. The plugin forgets a release
// declared for it earlier.
func (c *Client) AdoptFromWordPressOrg(ctx context.Context, install, kind, slug string, active bool) (*PackageState, error) {
	body := map[string]any{"source": "wordpress.org"}
	if kind == Plugins {
		body["active"] = active
	}
	encoded, _ := json.Marshal(body)
	var state PackageState
	return &state, c.do(ctx, install, http.MethodPut, "/"+kind+"/"+url.PathEscape(slug), encoded, "application/json", &state, true, nil)
}

// UploadRelease uploads a release zip that was checked against its declared
// SHA-256 (provider D7) for an installed package. A version other than the
// installed one is staged for WordPress's automatic updater and comes back
// as Pending (plugin D19). PHP parses a multipart body only for POST, so it's
// sent as POST with X-HTTP-Method-Override: PUT (plugin D16).
func (c *Client) UploadRelease(ctx context.Context, install, kind, slug string, zip []byte, version, sha256 string, active bool) (*PackageState, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	fields := map[string]string{"source": "release", "version": version, "sha256": sha256}
	if kind == Plugins {
		fields["active"] = strconv.FormatBool(active)
	}
	for name, value := range fields {
		_ = form.WriteField(name, value)
	}
	part, err := form.CreateFormFile("package", slug+".zip")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(zip); err != nil {
		return nil, err
	}
	if err := form.Close(); err != nil {
		return nil, err
	}
	var state PackageState
	return &state, c.do(ctx, install, http.MethodPost, "/"+kind+"/"+url.PathEscape(slug), body.Bytes(), form.FormDataContentType(), &state, true,
		map[string]string{"X-HTTP-Method-Override": http.MethodPut})
}

// RunCron requests the install's wp-cron.php, as a visitor's page load
// would, so WordPress's automatic updater installs a staged release (provider
// D21). Without doing_wp_cron, which runs only WordPress's own lock.
func (c *Client) RunCron(ctx context.Context, install string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL(install)+"/wp-cron.php", nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("running WP-Cron on %s: %w", install, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// GitHub's release download URLs, its API, and the origins the release
// token is sent to; tests point them elsewhere.
var (
	githubRelease = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/releases/download/([^/]+)/([^/?#]+)$`)
	githubAPI     = "https://api.github.com"
	tokenOrigins  = map[string]bool{"https://github.com": true, "https://api.github.com": true}
)

// DownloadRelease downloads a release zip and checks it against its declared
// SHA-256, with the release token for private GitHub releases. When
// github.com doesn't serve a GitHub release asset, the same asset is fetched
// through GitHub's API.
func DownloadRelease(ctx context.Context, httpClient *http.Client, location, sha256Hex, token string) ([]byte, error) {
	data, err := download(ctx, httpClient, location, token, "")
	if err != nil {
		if match := githubRelease.FindStringSubmatch(location); match != nil {
			// The URL's path is escaped: a tag like plugin/v1.0.0 is plugin%2Fv1.0.0.
			tag, tagErr := url.PathUnescape(match[3])
			name, nameErr := url.PathUnescape(match[4])
			if tagErr == nil && nameErr == nil {
				data, err = downloadGitHubAsset(ctx, httpClient, match[1], match[2], tag, name, token)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if actual := hex.EncodeToString(sum[:]); actual != sha256Hex {
		return nil, fmt.Errorf("the release zip at %s has SHA-256 %s, not the declared %s", location, actual, sha256Hex)
	}
	return data, nil
}

func downloadGitHubAsset(ctx context.Context, httpClient *http.Client, owner, repo, tag, name, token string) ([]byte, error) {
	release, err := download(ctx, httpClient, githubAPI+"/repos/"+owner+"/"+repo+"/releases/tags/"+url.PathEscape(tag), token, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(release, &parsed); err != nil {
		return nil, err
	}
	for _, asset := range parsed.Assets {
		if asset.Name == name {
			return download(ctx, httpClient, asset.URL, token, "application/octet-stream")
		}
	}
	return nil, fmt.Errorf("GitHub release %s of %s/%s has no asset %s", tag, owner, repo, name)
}

func download(ctx context.Context, httpClient *http.Client, location, token, accept string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	// The release token is GitHub's: only GitHub's own HTTPS origins get it.
	// Go drops it on redirects to other hosts, such as GitHub's asset storage.
	if token != "" && tokenOrigins[request.URL.Scheme+"://"+request.URL.Host] {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", location, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s answered HTTP %d", location, response.StatusCode)
	}
	return io.ReadAll(response.Body)
}

// HTTP is the client's HTTP client, for downloads.
func (c *Client) HTTP() *http.Client {
	return c.http
}
