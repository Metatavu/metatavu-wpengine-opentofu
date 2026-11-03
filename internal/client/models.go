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

// Domain is the Domain schema.
type Domain struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Primary              bool            `json:"primary"`
	Duplicate            bool            `json:"duplicate"`
	SecureAllURLs        bool            `json:"secure_all_urls"`
	RedirectsTo          []DomainTarget  `json:"redirects_to"`
	NetworkType          string          `json:"network_type"`
	NetworkDetails       *NetworkDetails `json:"network_details"`
	OwnershipStatus      string          `json:"ownership_status"`
	TXTVerificationKey   *string         `json:"txt_verification_key"`
	TXTVerificationValue *string         `json:"txt_verification_value"`
}

// DomainTarget is the domain a redirect goes to.
type DomainTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// NetworkDetails is how a domain is set up on WP Engine's network.
type NetworkDetails struct {
	DNSConfigInfo *struct {
		CNAME    string   `json:"cname"`
		ARecords []string `json:"a_records"`
	} `json:"dns_config_info"`
	NetworkInfo *struct {
		Status string `json:"status"`
		SSL    *struct {
			Status string `json:"status"`
		} `json:"ssl"`
	} `json:"network_info"`
}

// DomainCreate is createDomain's request.
type DomainCreate struct {
	Name       string  `json:"name"`
	Primary    bool    `json:"primary"`
	RedirectTo *string `json:"redirect_to,omitempty"`
}

// DomainUpdate is updateDomain's request.
type DomainUpdate struct {
	Primary       *bool   `json:"primary,omitempty"`
	RedirectTo    *string `json:"redirect_to,omitempty"`
	SecureAllURLs *bool   `json:"secure_all_urls,omitempty"`
}

// CacheExclusion is the CacheExclusion schema.
type CacheExclusion struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// CacheExclusionCookie is the type WP Engine manages itself: writes ignore it.
const CacheExclusionCookie = "cookie"

// CacheExclusionsUpdate is updateCacheExclusions' request.
type CacheExclusionsUpdate struct {
	Exclusions []CacheExclusion `json:"exclusions"`
}

// AccountUser is the AccountUser schema.
type AccountUser struct {
	UserID         string        `json:"user_id"`
	AccountID      string        `json:"account_id"`
	FirstName      string        `json:"first_name"`
	LastName       string        `json:"last_name"`
	Email          string        `json:"email"`
	Roles          string        `json:"roles"`
	InviteAccepted bool          `json:"invite_accepted"`
	MFAEnabled     bool          `json:"mfa_enabled"`
	Installs       []SiteInstall `json:"installs"`
}

// AccountUserCreate is createAccountUser's request.
type AccountUserCreate struct {
	User struct {
		AccountID  string   `json:"account_id"`
		FirstName  string   `json:"first_name"`
		LastName   string   `json:"last_name"`
		Email      string   `json:"email"`
		Roles      string   `json:"roles"`
		InstallIDs []string `json:"install_ids,omitempty"`
	} `json:"user"`
}

// AccountUserUpdate is updateAccountUser's request.
type AccountUserUpdate struct {
	Roles      string   `json:"roles"`
	InstallIDs []string `json:"install_ids,omitempty"`
}

// accountUserResponse wraps an account user that was created or updated.
type accountUserResponse struct {
	AccountUser AccountUser `json:"account_user"`
}

// SSHKey is the SshKey schema.
type SSHKey struct {
	UUID        string `json:"uuid"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment"`
	CreatedAt   string `json:"created_at"`
}

// SSHKeyCreate is createSshKey's request.
type SSHKeyCreate struct {
	PublicKey string `json:"public_key"`
}
