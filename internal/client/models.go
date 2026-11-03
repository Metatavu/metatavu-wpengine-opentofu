package client

// The WP Engine API's objects, with only the fields the provider uses. Each
// type names its OpenAPI schema; models_test.go checks every field against it.

// Reference is a nested object that the API gives only by ID.
type Reference struct {
	ID string `json:"id"`
}

// Site is the Site schema.
type Site struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Account  Reference     `json:"account"`
	Installs []SiteInstall `json:"installs"`
}

// SiteInstall is an install as a site lists it.
type SiteInstall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SiteCreate is createSite's request.
type SiteCreate struct {
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
}

// SiteUpdate is updateSite's request.
type SiteUpdate struct {
	Name string `json:"name"`
}

// Install is the Installation schema.
type Install struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Account       Reference  `json:"account"`
	Site          *Reference `json:"site"`
	Environment   *string    `json:"environment"`
	PHPVersion    *string    `json:"php_version"`
	Status        string     `json:"status"`
	CNAME         string     `json:"cname"`
	PrimaryDomain *string    `json:"primary_domain"`
	WPVersion     *string    `json:"wp_version"`
	IsMultisite   *bool      `json:"is_multisite"`
	StableIPs     []string   `json:"stable_ips"`
}

// Install statuses.
const (
	InstallActive  = "active"
	InstallPending = "pending"
)

// InstallCreate is createInstall's request.
type InstallCreate struct {
	Name        string  `json:"name"`
	AccountID   string  `json:"account_id"`
	SiteID      *string `json:"site_id,omitempty"`
	Environment *string `json:"environment,omitempty"`
}

// InstallUpdate is updateInstall's request.
type InstallUpdate struct {
	SiteID      *string `json:"site_id,omitempty"`
	Environment *string `json:"environment,omitempty"`
}

// page is a list operation's response.
type page[T any] struct {
	Next    *string `json:"next"`
	Results []T     `json:"results"`
}

// APIError is an error response.
type APIError struct {
	Status  int    `json:"-"`
	Message string `json:"message"`
	Errors  []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (e *APIError) Error() string {
	message := e.Message
	for _, field := range e.Errors {
		message += "; " + field.Field + ": " + field.Message
	}
	return message
}
