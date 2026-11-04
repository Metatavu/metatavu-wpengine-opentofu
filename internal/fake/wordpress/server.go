// Package wordpress is an in-memory stand-in for the metatavu-wordpress-iac
// mu-plugin's API on any number of installs, for the provider's tests. Each
// install is served at <URL>/<install>. Tokens are "test:<audience>:<nonce>":
// the audience must be the install's base URL, and a write token works once,
// as the plugin's jti check does. Values are stored as WordPress stores them.
package wordpress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	// StateReads counts GET /state by install, and StateReadsByToken by
	// install and read token: one provider process reuses its read token.
	StateReads        map[string]int
	StateReadsByToken map[string]map[string]int
	// Writes counts writes by install.
	Writes map[string]int
}

type install struct {
	options     map[string]bool // whitelisted name to sensitive
	themes      map[string]bool // whitelisted theme to installed
	values      map[string]any
	activeTheme string
}

// New starts a stand-in.
func New() *Server {
	s := &Server{installs: map[string]*install{}, used: map[string]bool{}, StateReads: map[string]int{}, StateReadsByToken: map[string]map[string]int{}, Writes: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// AddInstall serves an install with its whitelist: options (name to
// sensitive) and installed themes, the first of them active.
func (s *Server) AddInstall(name string, options map[string]bool, themes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := &install{options: options, themes: map[string]bool{}, values: map[string]any{}}
	for _, theme := range themes {
		in.themes[theme] = true
	}
	if len(themes) > 0 {
		in.activeTheme = themes[0]
	}
	s.installs[name] = in
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

	name := strings.Trim(r.URL.Path, "/")
	in, ok := s.installs[name]
	route := r.URL.Query().Get("rest_route")
	if !ok || !strings.HasPrefix(route, "/metatavu-iac/v1/") {
		writeError(w, http.StatusNotFound, "rest_no_route", "No route was found matching the URL and request method.")
		return
	}
	path := strings.TrimPrefix(route, "/metatavu-iac/v1")
	write := r.Method != http.MethodGet
	if code, message := s.authorize(r, name, write); code != 0 {
		writeError(w, code, "metatavu_iac_unauthorized", message)
		return
	}
	if write {
		s.Writes[name]++
	}

	switch {
	case r.Method == http.MethodGet && path == "/state":
		s.StateReads[name]++
		if s.StateReadsByToken[name] == nil {
			s.StateReadsByToken[name] = map[string]int{}
		}
		s.StateReadsByToken[name][r.Header.Get("Authorization")]++
		writeJSON(w, http.StatusOK, in.state(name))
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/options/"):
		option, _ := url.PathUnescape(strings.TrimPrefix(path, "/options/"))
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
	case r.Method == http.MethodPut && path == "/active-theme":
		var body struct {
			Slug string `json:"slug"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := in.themes[body.Slug]; !ok {
			writeError(w, http.StatusForbidden, "metatavu_iac_not_whitelisted", "Theme "+body.Slug+" isn't on the whitelist.")
			return
		}
		in.activeTheme = body.Slug
		writeJSON(w, http.StatusOK, map[string]string{"active_theme": body.Slug})
	default:
		writeError(w, http.StatusNotFound, "rest_no_route", "No route was found matching the URL and request method.")
	}
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
	return map[string]any{
		"install": name, "wp_version": "6.9", "php_version": "8.4.27",
		"options": options, "plugins": map[string]any{}, "themes": map[string]any{}, "active_theme": in.activeTheme,
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

// canonical is the plugin's canonical JSON: keys sorted at every level.
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
		encoded, _ := json.Marshal(v)
		return string(encoded)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": fmt.Sprint(message)})
}
