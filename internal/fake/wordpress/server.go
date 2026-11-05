// Package wordpress is an in-memory stand-in for the metatavu-wordpress-iac
// mu-plugin's API on any number of installs, for the provider's tests. Each
// install is served at <URL>/<install>. Tokens are "test:<audience>:<nonce>":
// the audience must be the install's base URL, and a write token works once,
// as the plugin's jti check does. Values are stored as WordPress stores them,
// and an uploaded release zip is checked against its declared SHA-256. A
// package must be installed first (Install, as the project's bootstrap/
// does); a release update is staged, and installed when <install>/wp-cron.php
// is requested, as WordPress's automatic updater does.
package wordpress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// TokenCommand prints a fresh test token for the provider's
// wordpress_token_command.
const TokenCommand = `echo "test:$IAC_AUDIENCE:$(od -An -N8 -tx8 /dev/urandom | tr -d ' \n')"`

// Server is the stand-in.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	installs map[string]*install
	used     map[string]bool

	// FailUpdates makes the automatic updater fail a slug's staged update with
	// the message, and HoldUpdates keeps it from running at all.
	FailUpdates map[string]string
	HoldUpdates bool
	// CronRuns counts requests to wp-cron.php by install.
	CronRuns map[string]int
	// StateReads counts GET /state by install, and StateReadsByToken by
	// install and read token: one provider process reuses its read token.
	StateReads        map[string]int
	StateReadsByToken map[string]map[string]int
	// Writes counts writes by install, and Uploads release uploads.
	Writes  map[string]int
	Uploads map[string]int
}

type install struct {
	options     map[string]bool // whitelisted name to sensitive
	values      map[string]any
	packages    map[string]map[string]*Package // kind to whitelisted slug
	activeTheme string
}

// Package is a whitelisted plugin's or theme's state, with a staged update
// that isn't installed yet and its last failed attempt.
type Package struct {
	Installed    bool
	Version      string
	Active       bool
	AutoUpdate   bool
	Pending      string
	PendingError string
}

// New starts a stand-in.
func New() *Server {
	s := &Server{
		installs: map[string]*install{}, used: map[string]bool{}, FailUpdates: map[string]string{}, CronRuns: map[string]int{},
		StateReads: map[string]int{}, StateReadsByToken: map[string]map[string]int{}, Writes: map[string]int{}, Uploads: map[string]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// AddInstall serves an install with its whitelisted options (name to
// sensitive) and installed, whitelisted themes, the first of them active.
func (s *Server) AddInstall(name string, options map[string]bool, themes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := &install{options: options, values: map[string]any{}, packages: map[string]map[string]*Package{"plugins": {}, "themes": {}}}
	for _, theme := range themes {
		in.packages["themes"][theme] = &Package{Installed: true, Version: "1.0"}
	}
	if len(themes) > 0 {
		in.activeTheme = themes[0]
	}
	s.installs[name] = in
}

// Whitelist adds packages (kind "plugins" or "themes") to an install's
// whitelist, not installed.
func (s *Server) Whitelist(installName, kind string, slugs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, slug := range slugs {
		s.installs[installName].packages[kind][slug] = &Package{}
	}
}

// Install installs a whitelisted package at a version, inactive, as the
// project's bootstrap/ does over SSH.
func (s *Server) Install(installName, kind, slug, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.installs[installName].packages[kind][slug] = Package{Installed: true, Version: version}
}

// Package returns a whitelisted package's state.
func (s *Server) Package(installName, kind, slug string) Package {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.installs[installName].packages[kind][slug]
}

// SetPackage changes a package as someone in wp-admin would.
func (s *Server) SetPackage(installName, kind, slug string, state Package) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.installs[installName].packages[kind][slug] = state
}

// SetSensitive changes an option's sensitivity in the whitelist, as the
// project's bootstrap/ would.
func (s *Server) SetSensitive(installName, name string, sensitive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installs[installName].options[name] = sensitive
}

// Endpoint is an install's base URL, for the provider's wordpress_endpoints.
func (s *Server) Endpoint(name string) string {
	return s.URL + "/" + name
}

// SetOption changes an option as someone in wp-admin would.
func (s *Server) SetOption(installName, name string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installs[installName].values[name] = stored(value)
}

// Option returns an option's stored value, and whether it exists.
func (s *Server) Option(installName, name string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.installs[installName].values[name]
	return value, ok
}

// ActiveTheme returns an install's active theme.
func (s *Server) ActiveTheme(installName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installs[installName].activeTheme
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name, ok := strings.CutSuffix(strings.Trim(r.URL.Path, "/"), "/wp-cron.php"); ok {
		if in, ok := s.installs[name]; ok {
			s.CronRuns[name]++
			s.runUpdates(in)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	name := strings.Trim(r.URL.Path, "/")
	in, ok := s.installs[name]
	route := r.URL.Query().Get("rest_route")
	if !ok || !strings.HasPrefix(route, "/metatavu-iac/v1/") {
		writeError(w, http.StatusNotFound, "rest_no_route", "No route was found matching the URL and request method.")
		return
	}
	path := strings.TrimPrefix(route, "/metatavu-iac/v1")
	// WordPress's REST server honours the override on POST (plugin D16).
	method := r.Method
	if override := r.Header.Get("X-HTTP-Method-Override"); method == http.MethodPost && override != "" {
		method = override
	}
	write := method != http.MethodGet
	if code, message := s.authorize(r, name, write); code != 0 {
		writeError(w, code, "metatavu_iac_unauthorized", message)
		return
	}
	if write {
		s.Writes[name]++
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case method == http.MethodGet && path == "/state":
		s.StateReads[name]++
		if s.StateReadsByToken[name] == nil {
			s.StateReadsByToken[name] = map[string]int{}
		}
		s.StateReadsByToken[name][r.Header.Get("Authorization")]++
		writeJSON(w, http.StatusOK, in.state(name))
	case method == http.MethodPut && len(parts) == 2 && parts[0] == "options":
		in.putOption(w, r, parts[1])
	case method == http.MethodPut && len(parts) == 2 && (parts[0] == "plugins" || parts[0] == "themes"):
		s.putPackage(w, r, in, name, parts[0], parts[1])
	case method == http.MethodPut && path == "/active-theme":
		var body struct {
			Slug string `json:"slug"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		theme, ok := in.packages["themes"][body.Slug]
		if !ok {
			writeError(w, http.StatusForbidden, "metatavu_iac_not_whitelisted", "Theme "+body.Slug+" isn't on the whitelist.")
			return
		}
		if !theme.Installed {
			writeError(w, http.StatusConflict, "metatavu_iac_not_installed", "Theme "+body.Slug+" isn't installed.")
			return
		}
		in.activeTheme = body.Slug
		writeJSON(w, http.StatusOK, map[string]string{"active_theme": body.Slug})
	default:
		writeError(w, http.StatusNotFound, "rest_no_route", "No route was found matching the URL and request method.")
	}
}

func (in *install) putOption(w http.ResponseWriter, r *http.Request, escaped string) {
	option, _ := url.PathUnescape(escaped)
	if _, ok := in.options[option]; !ok {
		writeError(w, http.StatusForbidden, "metatavu_iac_not_whitelisted", "Option "+option+" isn't on the whitelist.")
		return
	}
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "metatavu_iac_invalid", "The body is {\"value\": <JSON>}.")
		return
	}
	value, ok := body["value"]
	if !ok {
		writeError(w, http.StatusBadRequest, "metatavu_iac_invalid", "The body is {\"value\": <JSON>}.")
		return
	}
	in.values[option] = stored(value)
	writeJSON(w, http.StatusOK, in.option(option))
}

func (s *Server) putPackage(w http.ResponseWriter, r *http.Request, in *install, name, kind, slug string) {
	pkg, ok := in.packages[kind][slug]
	if !ok {
		writeError(w, http.StatusForbidden, "metatavu_iac_not_whitelisted", kind+" "+slug+" isn't on the whitelist.")
		return
	}
	if !pkg.Installed {
		writeError(w, http.StatusConflict, "metatavu_iac_not_installed", kind+" "+slug+" isn't installed: install it with the project's bootstrap/ first.")
		return
	}

	active := true
	// PHP parses a multipart body only for POST: a multipart PUT arrives empty.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if r.Method != http.MethodPost || r.ParseMultipartForm(64<<20) != nil {
			writeError(w, http.StatusBadRequest, "metatavu_iac_invalid", "source is wordpress.org or release. A release is multipart, sent as POST with X-HTTP-Method-Override: PUT.")
			return
		}
		file, _, err := r.FormFile("package")
		if err != nil {
			writeError(w, http.StatusBadRequest, "metatavu_iac_invalid", "A release needs its zip as the multipart field package.")
			return
		}
		data, _ := io.ReadAll(file)
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != r.FormValue("sha256") {
			writeError(w, http.StatusUnprocessableEntity, "metatavu_iac_checksum_mismatch", "The zip's SHA-256 isn't the declared one.")
			return
		}
		if r.FormValue("auto_update") != "" {
			writeError(w, http.StatusBadRequest, "metatavu_iac_invalid", "auto_update is for a wordpress.org package only: a release's version is declared.")
			return
		}
		if r.FormValue("active") != "" {
			active, _ = strconv.ParseBool(r.FormValue("active"))
		}
		// A version other than the installed one is staged for the automatic updater.
		pkg.Pending, pkg.PendingError = "", ""
		if version := r.FormValue("version"); version != pkg.Version {
			pkg.Pending = version
		}
		s.Uploads[name]++
	} else {
		var body struct {
			Source     string `json:"source"`
			Active     *bool  `json:"active"`
			AutoUpdate *bool  `json:"auto_update"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Source != "wordpress.org" {
			writeError(w, http.StatusBadRequest, "metatavu_iac_invalid", "source is wordpress.org or release.")
			return
		}
		if body.Active != nil {
			active = *body.Active
		}
		if body.AutoUpdate != nil {
			pkg.AutoUpdate = *body.AutoUpdate
		}
		// A release declared earlier is forgotten.
		pkg.Pending, pkg.PendingError = "", ""
	}
	pkg.Active = active && kind == "plugins"
	status := http.StatusOK
	if pkg.Pending != "" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, packageState(kind, pkg))
}

// runUpdates installs staged updates, as WordPress's automatic updater does
// on WP-Cron.
func (s *Server) runUpdates(in *install) {
	if s.HoldUpdates {
		return
	}
	for _, slugs := range in.packages {
		for slug, pkg := range slugs {
			if pkg.Pending == "" {
				continue
			}
			if message, fail := s.FailUpdates[slug]; fail {
				pkg.PendingError = message
				continue
			}
			pkg.Version, pkg.Pending, pkg.PendingError = pkg.Pending, "", ""
		}
	}
}

func packageState(kind string, pkg *Package) map[string]any {
	var version any
	if pkg.Installed {
		version = pkg.Version
	}
	state := map[string]any{"installed": pkg.Installed, "version": version, "auto_update": pkg.Installed && pkg.AutoUpdate}
	if kind == "plugins" {
		state["active"] = pkg.Active
	}
	if pkg.Pending != "" {
		var failure any
		if pkg.PendingError != "" {
			failure = pkg.PendingError
		}
		state["pending"] = map[string]any{"version": pkg.Pending, "error": failure}
	}
	return state
}

// authorize checks a test token: its audience, and that a write token is new.
func (s *Server) authorize(r *http.Request, name string, write bool) (int, string) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(token, ":")
	if !ok || len(parts) < 3 || parts[0] != "test" {
		return http.StatusUnauthorized, "A GitHub OIDC token is required."
	}
	audience := strings.Join(parts[1:len(parts)-1], ":")
	if audience != s.Endpoint(name) {
		return http.StatusUnauthorized, "The token is for another audience."
	}
	if write {
		if s.used[token] {
			return http.StatusUnauthorized, "The token was already used."
		}
		s.used[token] = true
	}
	return 0, ""
}

func (in *install) state(name string) map[string]any {
	options := map[string]any{}
	for option := range in.options {
		options[option] = in.option(option)
	}
	packages := map[string]map[string]any{"plugins": {}, "themes": {}}
	for kind, slugs := range in.packages {
		for slug, pkg := range slugs {
			packages[kind][slug] = packageState(kind, pkg)
		}
	}
	return map[string]any{
		"install": name, "wp_version": "6.9", "php_version": "8.4.27", "options": options,
		"plugins": packages["plugins"], "themes": packages["themes"], "active_theme": in.activeTheme,
	}
}

func (in *install) option(name string) map[string]any {
	value, exists := in.values[name]
	sensitive := in.options[name]
	state := map[string]any{"exists": exists, "sensitive": sensitive}
	switch {
	case exists && sensitive:
		sum := sha256.Sum256([]byte(canonical(value)))
		state["sha256"] = hex.EncodeToString(sum[:])
	case exists:
		state["value"] = value
	}
	return state
}

// stored is a value as WordPress stores an option: a top-level scalar as a
// string, and an object as a PHP array, so {} comes back as [].
func stored(value any) any {
	switch v := value.(type) {
	case nil:
		return ""
	case bool:
		if v {
			return "1"
		}
		return ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case map[string]any:
		if len(v) == 0 {
			return []any{}
		}
	}
	return value
}

// canonical is the plugin's canonical JSON: keys sorted at every level, and
// no HTML escaping, as PHP's json_encode.
func canonical(value any) string {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, key := range keys {
			parts[i] = canonical(key) + ":" + canonical(v[key])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = canonical(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		var encoded strings.Builder
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(v)
		return strings.TrimRight(encoded.String(), "\n")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
