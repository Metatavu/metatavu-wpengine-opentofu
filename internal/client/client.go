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
