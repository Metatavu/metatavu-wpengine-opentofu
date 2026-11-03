// Package wpengine is an in-memory stand-in for the WP Engine API v1, for the
// provider's tests. It answers like the API and refuses request fields that
// aren't in the API's OpenAPI document.
package wpengine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/openapi"
)

// The API user the server accepts.
const (
	Username = "test-user"
	Password = "test-password"
)

// DefaultPHPVersion is the PHP version new installs get.
const DefaultPHPVersion = "8.4"

var installName = regexp.MustCompile(`^[a-z][a-z0-9]{2,13}$`)

// Server is the stand-in.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	accounts map[string]bool
	sites    map[string]*client.Site
	installs map[string]*client.Install
	pending  map[string]int
	next     int

	// PendingReads is how many reads a new install stays pending for.
	PendingReads int
	// Calls counts requests by operation ID.
	Calls map[string]int
}

// New starts a stand-in.
func New() *Server {
	s := &Server{
		accounts: map[string]bool{},
		sites:    map[string]*client.Site{},
		installs: map[string]*client.Install{},
		pending:  map[string]int{},
		Calls:    map[string]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// AddAccount adds an account and returns its ID.
func (s *Server) AddAccount() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.id()
	s.accounts[id] = true
	return id
}

// AddSite records a site as if it existed already, and returns its ID.
func (s *Server) AddSite(accountID, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	site := &client.Site{ID: s.id(), Name: name, Account: client.Reference{ID: accountID}}
	s.sites[site.ID] = site
	return site.ID
}

// AddInstall records an active install as if it existed already, and returns
// its ID.
func (s *Server) AddInstall(accountID, siteID, name, environment string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	install := s.newInstall(name, accountID, &siteID, &environment)
	install.Status = client.InstallActive
	return install.ID
}

// SetPHPVersion changes an install's PHP version, as the portal does.
func (s *Server) SetPHPVersion(name, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, install := range s.installs {
		if install.Name == name {
			install.PHPVersion = &version
		}
	}
}

// Install returns an install's record by name.
func (s *Server) Install(name string) *client.Install {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, install := range s.installs {
		if install.Name == name {
			copied := *install
			return &copied
		}
	}
	return nil
}

// Site returns a site's record by name.
func (s *Server) Site(name string) *client.Site {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, site := range s.sites {
		if site.Name == name {
			copied := *site
			return &copied
		}
	}
	return nil
}

func (s *Server) id() string {
	s.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.next)
}

func (s *Server) newInstall(name, accountID string, siteID, environment *string) *client.Install {
	php := DefaultPHPVersion
	install := &client.Install{
		ID:          s.id(),
		Name:        name,
		Account:     client.Reference{ID: accountID},
		Environment: environment,
		PHPVersion:  &php,
		Status:      client.InstallPending,
		CNAME:       name + ".wpengine.com",
		StableIPs:   []string{},
	}
	if siteID != nil {
		install.Site = &client.Reference{ID: *siteID}
	}
	multisite := false
	install.IsMultisite = &multisite
	wp := "6.9"
	install.WPVersion = &wp
	s.installs[install.ID] = install
	s.pending[install.ID] = s.PendingReads
	return install
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if user, password, ok := r.BasicAuth(); !ok || user != Username || password != Password {
		writeError(w, http.StatusUnauthorized, "Bad Credentials")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/sites":
		s.call("listSites")
		sites := sorted(s.sites)
		for i := range sites {
			sites[i] = s.withInstalls(sites[i])
		}
		writePage(w, r, sites)
	case r.Method == http.MethodPost && r.URL.Path == "/sites":
		s.createSite(w, r)
	case len(parts) == 2 && parts[0] == "sites":
		s.site(w, r, parts[1])
	case r.Method == http.MethodGet && r.URL.Path == "/installs":
		s.call("listInstalls")
		writePage(w, r, sorted(s.installs))
	case r.Method == http.MethodPost && r.URL.Path == "/installs":
		s.createInstall(w, r)
	case len(parts) == 2 && parts[0] == "installs":
		s.install(w, r, parts[1])
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

func (s *Server) call(operation string) {
	s.Calls[operation]++
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	s.call("createSite")
	var request client.SiteCreate
	if !decode(w, r, "createSite", &request) {
		return
	}
	if request.Name == "" || !s.accounts[request.AccountID] {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	site := &client.Site{ID: s.id(), Name: request.Name, Account: client.Reference{ID: request.AccountID}}
	s.sites[site.ID] = site
	writeJSON(w, http.StatusCreated, s.withInstalls(site))
}

func (s *Server) site(w http.ResponseWriter, r *http.Request, id string) {
	site, ok := s.sites[id]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.call("getSite")
		writeJSON(w, http.StatusOK, s.withInstalls(site))
	case http.MethodPatch:
		s.call("updateSite")
		var request client.SiteUpdate
		if !decode(w, r, "updateSite", &request) {
			return
		}
		if request.Name != "" {
			site.Name = request.Name
		}
		writeJSON(w, http.StatusOK, s.withInstalls(site))
	case http.MethodDelete:
		s.call("deleteSite")
		delete(s.sites, id)
		for installID, install := range s.installs {
			if install.Site != nil && install.Site.ID == id {
				delete(s.installs, installID)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

// withInstalls is a copy of the site listing its installs, as the API does.
func (s *Server) withInstalls(site *client.Site) *client.Site {
	copied := *site
	copied.Installs = []client.SiteInstall{}
	for _, install := range sorted(s.installs) {
		if install.Site != nil && install.Site.ID == site.ID {
			copied.Installs = append(copied.Installs, client.SiteInstall{ID: install.ID, Name: install.Name})
		}
	}
	return &copied
}

func (s *Server) createInstall(w http.ResponseWriter, r *http.Request) {
	s.call("createInstall")
	var request client.InstallCreate
	if !decode(w, r, "createInstall", &request) {
		return
	}
	if !installName.MatchString(request.Name) || !s.accounts[request.AccountID] || !validEnvironment(request.Environment) {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if request.SiteID != nil {
		if _, ok := s.sites[*request.SiteID]; !ok {
			writeError(w, http.StatusBadRequest, "Invalid request.")
			return
		}
	}
	for _, install := range s.installs {
		if install.Name == request.Name {
			writeError(w, http.StatusBadRequest, "Name is already taken.")
			return
		}
	}
	install := s.newInstall(request.Name, request.AccountID, request.SiteID, request.Environment)
	writeJSON(w, http.StatusCreated, install)
}

func (s *Server) install(w http.ResponseWriter, r *http.Request, id string) {
	install, ok := s.installs[id]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.call("getInstall")
		if s.pending[id] > 0 {
			s.pending[id]--
		} else {
			install.Status = client.InstallActive
		}
		writeJSON(w, http.StatusOK, install)
	case http.MethodPatch:
		s.call("updateInstall")
		var request client.InstallUpdate
		if !decode(w, r, "updateInstall", &request) {
			return
		}
		if !validEnvironment(request.Environment) {
			writeError(w, http.StatusBadRequest, "Invalid request.")
			return
		}
		if request.SiteID != nil {
			if _, ok := s.sites[*request.SiteID]; !ok {
				writeError(w, http.StatusBadRequest, "Invalid request.")
				return
			}
			install.Site = &client.Reference{ID: *request.SiteID}
		}
		if request.Environment != nil {
			install.Environment = request.Environment
		}
		writeJSON(w, http.StatusOK, install)
	case http.MethodDelete:
		s.call("deleteInstall")
		delete(s.installs, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func validEnvironment(environment *string) bool {
	if environment == nil {
		return true
	}
	switch *environment {
	case "production", "staging", "development":
		return true
	}
	return false
}

// decode reads a request body, refusing fields the operation doesn't have in
// the OpenAPI document.
func decode(w http.ResponseWriter, r *http.Request, operation string, out any) bool {
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON.")
		return false
	}
	for _, field := range flatten(raw, "") {
		if !openapi.HasRequestField(operation, field) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s has no field %s in the OpenAPI document", operation, field))
			return false
		}
	}
	encoded, _ := json.Marshal(raw)
	if err := json.Unmarshal(encoded, out); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return false
	}
	return true
}

func flatten(value map[string]any, prefix string) []string {
	var out []string
	for key, v := range value {
		out = append(out, prefix+key)
		if nested, ok := v.(map[string]any); ok {
			out = append(out, flatten(nested, prefix+key+".")...)
		}
	}
	return out
}

func sorted[T any](values map[string]*T) []*T {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*T, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

// writePage answers a list operation with the API's limit and offset paging.
func writePage[T any](w http.ResponseWriter, r *http.Request, all []T) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset > len(all) {
		offset = len(all)
	}
	end := min(offset+limit, len(all))
	var next *string
	if end < len(all) {
		link := fmt.Sprintf("%s?limit=%d&offset=%d", r.URL.Path, limit, end)
		next = &link
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(all), "next": next, "previous": nil, "results": all[offset:end]})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}
