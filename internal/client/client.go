// Package client calls the WP Engine API v1.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is the WP Engine API.
const DefaultURL = "https://api.wpengineapi.com/v1"

// Client calls the API as one API user.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client

	// Retries is how many times a request that answered 429 or 503 is sent
	// again, and RetryWait how long to wait when the response doesn't say.
	Retries   int
	RetryWait time.Duration
}

// New returns a client for the API at baseURL, logging in with the API
// user's credentials from the portal's API Access page.
func New(baseURL, username, password string, httpClient *http.Client) (*Client, error) {
	if username == "" || password == "" {
		return nil, errors.New("give both the API username and the API password")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		username:  username,
		password:  password,
		http:      httpClient,
		Retries:   5,
		RetryWait: 5 * time.Second,
	}, nil
}

// IsNotFound tells whether err is the API's 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// GetSite reads a site.
func (c *Client) GetSite(ctx context.Context, id string) (*Site, error) {
	var site Site
	return &site, c.do(ctx, http.MethodGet, "/sites/"+url.PathEscape(id), nil, &site)
}

// ListSites reads every site the API user can see.
func (c *Client) ListSites(ctx context.Context) ([]Site, error) {
	return list[Site](ctx, c, "/sites")
}

// CreateSite creates a site.
func (c *Client) CreateSite(ctx context.Context, request SiteCreate) (*Site, error) {
	var site Site
	return &site, c.do(ctx, http.MethodPost, "/sites", request, &site)
}

// UpdateSite renames a site.
func (c *Client) UpdateSite(ctx context.Context, id string, request SiteUpdate) (*Site, error) {
	var site Site
	return &site, c.do(ctx, http.MethodPatch, "/sites/"+url.PathEscape(id), request, &site)
}

// DeleteSite deletes a site and every install in it.
func (c *Client) DeleteSite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/sites/"+url.PathEscape(id), nil, nil)
}

// GetInstall reads an install.
func (c *Client) GetInstall(ctx context.Context, id string) (*Install, error) {
	var install Install
	return &install, c.do(ctx, http.MethodGet, "/installs/"+url.PathEscape(id), nil, &install)
}

// ListInstalls reads every install the API user can see.
func (c *Client) ListInstalls(ctx context.Context) ([]Install, error) {
	return list[Install](ctx, c, "/installs")
}

// CreateInstall creates an install.
func (c *Client) CreateInstall(ctx context.Context, request InstallCreate) (*Install, error) {
	var install Install
	return &install, c.do(ctx, http.MethodPost, "/installs", request, &install)
}

// UpdateInstall moves an install to another site or environment type.
func (c *Client) UpdateInstall(ctx context.Context, id string, request InstallUpdate) (*Install, error) {
	var install Install
	return &install, c.do(ctx, http.MethodPatch, "/installs/"+url.PathEscape(id), request, &install)
}

// DeleteInstall deletes an install. There's no undo.
func (c *Client) DeleteInstall(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/installs/"+url.PathEscape(id), nil, nil)
}

// ListDomains reads an install's domains.
func (c *Client) ListDomains(ctx context.Context, installID string) ([]Domain, error) {
	return list[Domain](ctx, c, "/installs/"+url.PathEscape(installID)+"/domains")
}

// GetDomain reads a domain of an install.
func (c *Client) GetDomain(ctx context.Context, installID, id string) (*Domain, error) {
	var domain Domain
	return &domain, c.do(ctx, http.MethodGet, "/installs/"+url.PathEscape(installID)+"/domains/"+url.PathEscape(id), nil, &domain)
}

// CreateDomain adds a domain or a redirect to an install.
func (c *Client) CreateDomain(ctx context.Context, installID string, request DomainCreate) (*Domain, error) {
	var domain Domain
	return &domain, c.do(ctx, http.MethodPost, "/installs/"+url.PathEscape(installID)+"/domains", request, &domain)
}

// UpdateDomain changes a domain of an install.
func (c *Client) UpdateDomain(ctx context.Context, installID, id string, request DomainUpdate) (*Domain, error) {
	var domain Domain
	return &domain, c.do(ctx, http.MethodPatch, "/installs/"+url.PathEscape(installID)+"/domains/"+url.PathEscape(id), request, &domain)
}

// DeleteDomain removes a domain from an install.
func (c *Client) DeleteDomain(ctx context.Context, installID, id string) error {
	return c.do(ctx, http.MethodDelete, "/installs/"+url.PathEscape(installID)+"/domains/"+url.PathEscape(id), nil, nil)
}

// ListCacheExclusions reads an install's cache exclusions, WP Engine's own
// cookie exclusions included.
func (c *Client) ListCacheExclusions(ctx context.Context, installID string) ([]CacheExclusion, error) {
	return list[CacheExclusion](ctx, c, "/installs/"+url.PathEscape(installID)+"/cache_exclusions")
}

// SetCacheExclusions replaces an install's writable cache exclusions.
func (c *Client) SetCacheExclusions(ctx context.Context, installID string, exclusions []CacheExclusion) error {
	return c.do(ctx, http.MethodPut, "/installs/"+url.PathEscape(installID)+"/cache_exclusions", CacheExclusionsUpdate{Exclusions: exclusions}, nil)
}

// GetAccountUser reads a user of an account.
func (c *Client) GetAccountUser(ctx context.Context, accountID, userID string) (*AccountUser, error) {
	var user AccountUser
	return &user, c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/account_users/"+url.PathEscape(userID), nil, &user)
}

// ListAccountUsers reads an account's users.
func (c *Client) ListAccountUsers(ctx context.Context, accountID string) ([]AccountUser, error) {
	var p page[AccountUser]
	return p.Results, c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/account_users", nil, &p)
}

// CreateAccountUser adds a user to an account. WP Engine emails them an
// invitation.
func (c *Client) CreateAccountUser(ctx context.Context, accountID string, request AccountUserCreate) (*AccountUser, error) {
	var response accountUserResponse
	return &response.AccountUser, c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/account_users", request, &response)
}

// UpdateAccountUser changes a user's roles and installs.
func (c *Client) UpdateAccountUser(ctx context.Context, accountID, userID string, request AccountUserUpdate) (*AccountUser, error) {
	var response accountUserResponse
	return &response.AccountUser, c.do(ctx, http.MethodPatch, "/accounts/"+url.PathEscape(accountID)+"/account_users/"+url.PathEscape(userID), request, &response)
}

// DeleteAccountUser removes a user from an account.
func (c *Client) DeleteAccountUser(ctx context.Context, accountID, userID string) error {
	return c.do(ctx, http.MethodDelete, "/accounts/"+url.PathEscape(accountID)+"/account_users/"+url.PathEscape(userID), nil, nil)
}

// ListSSHKeys reads the API user's SSH keys.
func (c *Client) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	return list[SSHKey](ctx, c, "/ssh_keys")
}

// CreateSSHKey adds an SSH key to the API user.
func (c *Client) CreateSSHKey(ctx context.Context, publicKey string) (*SSHKey, error) {
	var key SSHKey
	return &key, c.do(ctx, http.MethodPost, "/ssh_keys", SSHKeyCreate{PublicKey: publicKey}, &key)
}

// DeleteSSHKey removes one of the API user's SSH keys.
func (c *Client) DeleteSSHKey(ctx context.Context, uuid string) error {
	return c.do(ctx, http.MethodDelete, "/ssh_keys/"+url.PathEscape(uuid), nil, nil)
}

// list follows a list operation's pages.
func list[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	for offset := 0; ; {
		var p page[T]
		query := url.Values{"limit": {"100"}, "offset": {strconv.Itoa(offset)}}
		if err := c.do(ctx, http.MethodGet, path+"?"+query.Encode(), nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Results...)
		if p.Next == nil || *p.Next == "" || len(p.Results) == 0 {
			return all, nil
		}
		offset += len(p.Results)
	}
}

// do sends a request. Bodies are never logged: they can hold secrets.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var encoded []byte
	if body != nil {
		var err error
		if encoded, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if encoded != nil {
			reader = bytes.NewReader(encoded)
		}
		request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return err
		}
		request.SetBasicAuth(c.username, c.password)
		request.Header.Set("Accept", "application/json")
		if encoded != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := c.http.Do(request)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return err
		}
		if (response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable) && attempt < c.Retries {
			if err := sleep(ctx, retryAfter(response, c.RetryWait)); err != nil {
				return err
			}
			continue
		}
		if response.StatusCode >= 300 {
			apiErr := &APIError{}
			if json.Unmarshal(raw, apiErr) != nil || apiErr.Message == "" {
				apiErr.Message = fmt.Sprintf("%s %s answered HTTP %d", method, path, response.StatusCode)
			}
			apiErr.Status = response.StatusCode
			return apiErr
		}
		if out != nil && len(raw) > 0 {
			return json.Unmarshal(raw, out)
		}
		return nil
	}
}

func retryAfter(response *http.Response, fallback time.Duration) time.Duration {
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
