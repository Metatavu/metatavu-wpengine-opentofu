package wordpress

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// State is GET /state.
type State struct {
	Install     string                 `json:"install"`
	WPVersion   string                 `json:"wp_version"`
	PHPVersion  string                 `json:"php_version"`
	Options     map[string]OptionState `json:"options"`
	Plugins     map[string]PluginState `json:"plugins"`
	Themes      map[string]ThemeState  `json:"themes"`
	ActiveTheme string                 `json:"active_theme"`
}

// OptionState is a whitelisted option: its value, or a sensitive one's hash.
type OptionState struct {
	Exists    bool            `json:"exists"`
	Sensitive bool            `json:"sensitive"`
	Value     json.RawMessage `json:"value,omitempty"`
	SHA256    string          `json:"sha256,omitempty"`
}

// PluginState is a whitelisted plugin.
type PluginState struct {
	Installed  bool     `json:"installed"`
	Version    *string  `json:"version"`
	Active     bool     `json:"active"`
	AutoUpdate bool     `json:"auto_update"`
	Pending    *Pending `json:"pending"`
}

// ThemeState is a whitelisted theme.
type ThemeState struct {
	Installed  bool     `json:"installed"`
	Version    *string  `json:"version"`
	AutoUpdate bool     `json:"auto_update"`
	Pending    *Pending `json:"pending"`
}

// Pending is a declared release that WordPress's automatic updater hasn't
// installed yet, with its last failed attempt (plugin D19).
type Pending struct {
	Version string  `json:"version"`
	Error   *string `json:"error"`
}

// APIError is the plugin's error: {"code", "message"}.
type APIError struct {
	Status  int    `json:"-"`
	Install string `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	message := e.Message
	switch e.Code {
	case "metatavu_iac_not_whitelisted":
		message += " Add it to the install's whitelist in the project's bootstrap/ and apply that first."
	case "metatavu_iac_not_configured":
		message += " Install the IaC plugin with its config.php through the project's bootstrap/."
	}
	if e.Status == http.StatusNotFound && strings.Contains(e.Code, "rest_no_route") {
		message = "The IaC plugin isn't installed on " + e.Install + ": install it through the project's bootstrap/."
	}
	return message
}

// IsCode tells whether err is the plugin's error with this code.
func IsCode(err error, code string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}
