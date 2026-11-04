// Package wordpress calls the metatavu-wordpress-iac mu-plugin's API on WP
// Engine installs, with GitHub Actions OIDC tokens.
package wordpress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Namespace is the plugin's REST namespace.
const Namespace = "/metatavu-iac/v1"

// maintenanceWait is how long a request waits out WordPress's maintenance
// mode, which covers the whole site while its automatic updater installs a
// staged release (provider D21): the request is tried again every interval,
// until timeout. Tests shorten it.
var maintenanceWait = struct{ interval, timeout time.Duration }{5 * time.Second, 3 * time.Minute}

// errMaintenance is WordPress's maintenance page instead of the plugin's answer.
var errMaintenance = errors.New("the site is in WordPress's maintenance mode")

// ErrNotInCI is the error outside GitHub Actions (provider D5).
var ErrNotInCI = errors.New("site/ is applied only by CI: the WordPress resources need a GitHub Actions OIDC token, which exists only in a workflow with `permissions: id-token: write`")

// TokenSource gets a token whose audience is an install's base URL.
type TokenSource interface {
	Token(ctx context.Context, audience string) (string, error)
}

// ActionsTokens requests GitHub Actions OIDC tokens; nil outside Actions.
func ActionsTokens(httpClient *http.Client) TokenSource {
	requestURL, requestToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if requestURL == "" || requestToken == "" {
		return nil
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: time.Minute}
	}
	return &actionsTokens{url: requestURL, token: requestToken, http: httpClient}
}

type actionsTokens struct {
	url, token string
	http       *http.Client
}

func (a *actionsTokens) Token(ctx context.Context, audience string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url+"&audience="+url.QueryEscape(audience), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "bearer "+a.token)
	response, err := a.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("requesting a GitHub OIDC token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("requesting a GitHub OIDC token answered HTTP %d", response.StatusCode)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Value == "" {
		return "", errors.New("GitHub answered no OIDC token")
	}
	return body.Value, nil
}

// CommandTokens runs a command for each token, for tests: it gets the
// audience in IAC_AUDIENCE and prints the token.
func CommandTokens(command string) TokenSource {
	return commandTokens(command)
}

type commandTokens string

func (c commandTokens) Token(ctx context.Context, audience string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", string(c))
	cmd.Env = append(os.Environ(), "IAC_AUDIENCE="+audience)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("wordpress_token_command failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" || strings.ContainsAny(token, " \n") {
		return "", errors.New("wordpress_token_command must print one token and nothing else")
	}
	return token, nil
}

// Client calls the plugin on any install.
type Client struct {
	endpoints map[string]string
	tokens    TokenSource
	http      *http.Client

	mu         sync.Mutex
	installs   map[string]*installCache
	readTokens map[string]cachedToken
}

// installCache is one install's state. Reading holds lock, so concurrent
// reads wait for one request; generation changes around every write, so a
// read that overlapped a write isn't kept.
type installCache struct {
	lock       sync.Mutex
	state      *State
	generation int

	// sensitive is the whitelist's options and their sensitivity, from the
	// last state read. Only bootstrap/ changes it, never a write, so it
	// outlives the state.
	sensitive map[string]bool

	// writing lets one write at a time reach the install: WordPress keeps
	// shared state such as active_plugins in single options, and concurrent
	// activations would overwrite each other's.
	writing sync.Mutex
}

type cachedToken struct {
	value   string
	expires time.Time
}

// New returns a client. endpoints overrides installs' base URLs, for tests;
// tokens is nil outside CI, where every call fails with ErrNotInCI.
func New(endpoints map[string]string, tokens TokenSource, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{endpoints: endpoints, tokens: tokens, http: httpClient, installs: map[string]*installCache{}, readTokens: map[string]cachedToken{}}
}

// BaseURL is an install's base URL, the tokens' audience (provider D4).
func (c *Client) BaseURL(install string) string {
	if endpoint, ok := c.endpoints[install]; ok {
		return strings.TrimRight(endpoint, "/")
	}
	return "https://" + install + ".wpengine.com"
}

// State reads an install's managed state, once per plan (provider D11):
// writes invalidate it.
func (c *Client) State(ctx context.Context, install string) (*State, error) {
	cache := c.cache(install)
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if cache.state != nil {
		return cache.state, nil
	}
	generation := cache.generation
	var state State
	if err := c.do(ctx, install, http.MethodGet, "/state", nil, "", &state, false, nil); err != nil {
		return nil, err
	}
	if cache.generation == generation {
		cache.state = &state
	}
	cache.sensitive = map[string]bool{}
	for name, option := range state.Options {
		cache.sensitive[name] = option.Sensitive
	}
	return &state, nil
}

// FreshState reads an install's state again, for waiting on WordPress.
func (c *Client) FreshState(ctx context.Context, install string) (*State, error) {
	c.invalidate(install)
	return c.State(ctx, install)
}

// Sensitive tells which whitelisted options are sensitive: name to
// sensitivity. It reads the state only when no read has happened yet.
func (c *Client) Sensitive(ctx context.Context, install string) (map[string]bool, error) {
	cache := c.cache(install)
	cache.lock.Lock()
	known := cache.sensitive
	cache.lock.Unlock()
	if known != nil {
		return known, nil
	}
	if _, err := c.State(ctx, install); err != nil {
		return nil, err
	}
	cache.lock.Lock()
	defer cache.lock.Unlock()
	return cache.sensitive, nil
}

func (c *Client) cache(install string) *installCache {
	c.mu.Lock()
	defer c.mu.Unlock()
	cache, ok := c.installs[install]
	if !ok {
		cache = &installCache{}
		c.installs[install] = cache
	}
	return cache
}

// invalidate forgets an install's state, before and after a write.
func (c *Client) invalidate(install string) {
	cache := c.cache(install)
	cache.lock.Lock()
	cache.state = nil
	cache.generation++
	cache.lock.Unlock()
}

// PutOption sets a whitelisted option to a normalised value.
func (c *Client) PutOption(ctx context.Context, install, name string, value any) (*OptionState, error) {
	var option OptionState
	body, _ := json.Marshal(map[string]any{"value": value})
	return &option, c.do(ctx, install, http.MethodPut, "/options/"+url.PathEscape(name), body, "application/json", &option, true, nil)
}

// PutActiveTheme activates a whitelisted, installed theme.
func (c *Client) PutActiveTheme(ctx context.Context, install, slug string) error {
	body, _ := json.Marshal(map[string]string{"slug": slug})
	return c.do(ctx, install, http.MethodPut, "/active-theme", body, "application/json", nil, true, nil)
}

// do calls the plugin through ?rest_route= (provider D18), which works with
// any permalink setting. A write gets a fresh token (provider D17) and
// invalidates the install's cached state. While WordPress's maintenance mode
// answers instead of the plugin, the request is tried again. Bodies are
// never logged.
func (c *Client) do(ctx context.Context, install, method, path string, body []byte, contentType string, out any, write bool, headers map[string]string) error {
	if c.tokens == nil {
		return ErrNotInCI
	}
	// A write waits for its turn before it gets its token, so a long queue of
	// slow installs can't outlast the plugin's five-minute window.
	if write {
		cache := c.cache(install)
		cache.writing.Lock()
		defer cache.writing.Unlock()
		c.invalidate(install)
		defer c.invalidate(install)
	}
	deadline := time.Now().Add(maintenanceWait.timeout)
	for {
		err := c.attempt(ctx, install, method, path, body, contentType, out, write, headers)
		if !errors.Is(err, errMaintenance) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s %s on %s: %w for longer than %s, while WordPress installs an update; try again once it's done", method, path, install, err, maintenanceWait.timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(maintenanceWait.interval):
		}
	}
}

// attempt makes one request. WordPress answers its maintenance page before it
// loads any plugin, so a write token sent then isn't used up; the next
// attempt gets a fresh one all the same.
func (c *Client) attempt(ctx context.Context, install, method, path string, body []byte, contentType string, out any, write bool, headers map[string]string) error {
	token, err := c.token(ctx, install, write)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	endpoint := c.BaseURL(install) + "/?rest_route=" + url.QueryEscape(Namespace+path)
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%s %s on %s: %w", method, path, install, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	// WordPress's maintenance answer, as HTML or, to a request accepting JSON,
	// as wp_die's JSON. The plugin's own 503s say something else.
	if response.StatusCode == http.StatusServiceUnavailable && bytes.Contains(raw, []byte("Briefly unavailable for scheduled maintenance")) {
		return errMaintenance
	}
	if response.StatusCode >= 300 {
		apiErr := &APIError{Status: response.StatusCode, Install: install}
		if json.Unmarshal(raw, apiErr) != nil || apiErr.Message == "" {
			apiErr.Message = fmt.Sprintf("%s %s answered HTTP %d", method, path, response.StatusCode)
		}
		return apiErr
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			if json.Valid(raw) {
				return fmt.Errorf("the IaC plugin on %s answered %s %s with an unexpected response: %w", install, method, path, err)
			}
			return fmt.Errorf("the IaC plugin on %s answered %s %s with something other than JSON, starting %q: "+
				"something on the site printed output before the response, such as a PHP notice or a database error shown with WP_DEBUG; see the site's error log",
				install, method, path, preview(raw))
		}
	}
	return nil
}

// preview is the start of a response, for an error message.
func preview(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	if len(text) > 120 {
		text = text[:120] + "…"
	}
	return text
}

// token returns a write token fresh, and a read token reused while it has a
// minute left of the plugin's five-minute window.
func (c *Client) token(ctx context.Context, install string, write bool) (string, error) {
	audience := c.BaseURL(install)
	if !write {
		c.mu.Lock()
		cached, ok := c.readTokens[install]
		c.mu.Unlock()
		if ok && time.Now().Before(cached.expires) {
			return cached.value, nil
		}
	}
	token, err := c.tokens.Token(ctx, audience)
	if err != nil {
		return "", err
	}
	if !write {
		c.mu.Lock()
		c.readTokens[install] = cachedToken{value: token, expires: readTokenExpiry(token)}
		c.mu.Unlock()
	}
	return token, nil
}

// readTokenExpiry is when a read token stops being reused: a minute before
// the plugin stops accepting it (issued at most five minutes ago), or its
// exp, whichever comes first.
func readTokenExpiry(token string) time.Time {
	limit := time.Now().Add(4 * time.Minute)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return limit
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return limit
	}
	var claims struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return limit
	}
	if claims.Iat > 0 && time.Unix(claims.Iat, 0).Add(4*time.Minute).Before(limit) {
		limit = time.Unix(claims.Iat, 0).Add(4 * time.Minute)
	}
	if claims.Exp > 0 && time.Unix(claims.Exp, 0).Add(-time.Minute).Before(limit) {
		limit = time.Unix(claims.Exp, 0).Add(-time.Minute)
	}
	return limit
}
