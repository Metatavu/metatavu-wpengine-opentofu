package wpengine

import (
	"net/http"
	"strings"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"golang.org/x/crypto/ssh"
)

// SetCookieExclusion adds a cookie exclusion, as WP Engine does itself.
func (s *Server) SetCookieExclusion(installName, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, install := range s.installs {
		if install.Name == installName {
			s.exclusions[id] = append(s.exclusions[id], client.CacheExclusion{Type: client.CacheExclusionCookie, Value: value})
		}
	}
}

// Domain returns a domain's record by its install's name and its own.
func (s *Server) Domain(installName, name string) *client.Domain {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, install := range s.installs {
		if install.Name != installName {
			continue
		}
		for _, domain := range s.domains[id] {
			if domain.Name == name {
				copied := *domain
				return &copied
			}
		}
	}
	return nil
}

// AccountUser returns an account user's record by email.
func (s *Server) AccountUser(accountID, email string) *client.AccountUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users[accountID] {
		if user.Email == email {
			copied := *user
			return &copied
		}
	}
	return nil
}

// SSHKeys counts the API user's SSH keys.
func (s *Server) SSHKeys() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sshKeys)
}

func (s *Server) domain(w http.ResponseWriter, r *http.Request, installID string, rest []string) {
	if _, ok := s.installs[installID]; !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	domains := s.domains[installID]
	if domains == nil {
		domains = map[string]*client.Domain{}
		s.domains[installID] = domains
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.call("listDomains")
			writePage(w, r, sorted(domains))
		case http.MethodPost:
			s.call("createDomain")
			var request client.DomainCreate
			if !decode(w, r, "createDomain", &request) {
				return
			}
			for _, existing := range domains {
				if existing.Name == request.Name {
					writeError(w, http.StatusBadRequest, "Domain already exists.")
					return
				}
			}
			pendingKey, pendingValue := "_wpengine."+request.Name, "verify-"+request.Name
			domain := &client.Domain{
				ID:                   s.id(),
				Name:                 request.Name,
				NetworkType:          "AN",
				OwnershipStatus:      "TXT_VERIFICATION_PENDING",
				TXTVerificationKey:   &pendingKey,
				TXTVerificationValue: &pendingValue,
				RedirectsTo:          []client.DomainTarget{},
			}
			if !s.redirect(w, domains, domain, request.RedirectTo) {
				return
			}
			domains[domain.ID] = domain
			if request.Primary {
				s.makePrimary(domains, domain)
			}
			writeJSON(w, http.StatusCreated, domain)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
		return
	}
	domain, ok := domains[rest[0]]
	if !ok || len(rest) > 1 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.call("getDomain")
		writeJSON(w, http.StatusOK, domain)
	case http.MethodPatch:
		s.call("updateDomain")
		var request client.DomainUpdate
		if !decode(w, r, "updateDomain", &request) {
			return
		}
		if request.RedirectTo != nil && !s.redirect(w, domains, domain, request.RedirectTo) {
			return
		}
		if request.SecureAllURLs != nil {
			domain.SecureAllURLs = *request.SecureAllURLs
		}
		if request.Primary != nil {
			if *request.Primary {
				if len(domain.RedirectsTo) > 0 {
					writeError(w, http.StatusBadRequest, "A redirected domain can't be the primary.")
					return
				}
				s.makePrimary(domains, domain)
			} else {
				domain.Primary = false
			}
		}
		writeJSON(w, http.StatusOK, domain)
	case http.MethodDelete:
		s.call("deleteDomain")
		delete(domains, domain.ID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func (s *Server) redirect(w http.ResponseWriter, domains map[string]*client.Domain, domain *client.Domain, target *string) bool {
	if target == nil {
		return true
	}
	to, ok := domains[*target]
	if !ok || to.ID == domain.ID {
		writeError(w, http.StatusBadRequest, "Invalid redirect.")
		return false
	}
	domain.RedirectsTo = []client.DomainTarget{{ID: to.ID, Name: to.Name}}
	return true
}

func (s *Server) makePrimary(domains map[string]*client.Domain, primary *client.Domain) {
	for _, domain := range domains {
		domain.Primary = domain.ID == primary.ID
	}
}

func (s *Server) cacheExclusions(w http.ResponseWriter, r *http.Request, installID string) {
	if _, ok := s.installs[installID]; !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.call("listCacheExclusions")
		writePage(w, r, append([]client.CacheExclusion{}, s.exclusions[installID]...))
	case http.MethodPut:
		s.call("updateCacheExclusions")
		var request client.CacheExclusionsUpdate
		if !decode(w, r, "updateCacheExclusions", &request) {
			return
		}
		// Cookie exclusions are WP Engine's: accepted and ignored. Duplicates collapse.
		kept := []client.CacheExclusion{}
		seen := map[client.CacheExclusion]bool{}
		for _, exclusion := range s.exclusions[installID] {
			if exclusion.Type == client.CacheExclusionCookie {
				kept = append(kept, exclusion)
			}
		}
		for _, exclusion := range request.Exclusions {
			switch exclusion.Type {
			case client.CacheExclusionCookie:
				continue
			case "query arg", "url path":
			default:
				writeError(w, http.StatusBadRequest, "Invalid exclusion type.")
				return
			}
			if !seen[exclusion] {
				seen[exclusion] = true
				kept = append(kept, exclusion)
			}
		}
		s.exclusions[installID] = kept
		writeJSON(w, http.StatusOK, map[string]any{"results": kept})
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

var roles = map[string]bool{"owner": true, "full,billing": true, "full": true, "partial,billing": true, "partial": true}

func (s *Server) accountUser(w http.ResponseWriter, r *http.Request, accountID string, rest []string) {
	users, ok := s.users[accountID]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.call("listAccountUsers")
			writeJSON(w, http.StatusOK, map[string]any{"results": sorted(users)})
		case http.MethodPost:
			s.call("createAccountUser")
			var request client.AccountUserCreate
			if !decode(w, r, "createAccountUser", &request) {
				return
			}
			u := request.User
			if u.AccountID != accountID || u.Email == "" || !roles[u.Roles] {
				writeError(w, http.StatusBadRequest, "Invalid request.")
				return
			}
			for _, existing := range users {
				if strings.EqualFold(existing.Email, u.Email) {
					writeError(w, http.StatusBadRequest, "User already exists.")
					return
				}
			}
			user := &client.AccountUser{UserID: s.id(), AccountID: accountID, FirstName: u.FirstName, LastName: u.LastName, Email: u.Email}
			if !s.setRoles(w, user, u.Roles, u.InstallIDs) {
				return
			}
			users[user.UserID] = user
			writeJSON(w, http.StatusCreated, map[string]any{"message": "User created", "account_user": user})
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
		return
	}
	user, ok := users[rest[0]]
	if !ok || len(rest) > 1 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.call("getAccountUser")
		writeJSON(w, http.StatusOK, user)
	case http.MethodPatch:
		s.call("updateAccountUser")
		var request client.AccountUserUpdate
		if !decode(w, r, "updateAccountUser", &request) {
			return
		}
		if !roles[request.Roles] || !s.setRoles(w, user, request.Roles, request.InstallIDs) {
			if !roles[request.Roles] {
				writeError(w, http.StatusBadRequest, "Invalid roles.")
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": "User updated", "account_user": user})
	case http.MethodDelete:
		s.call("deleteAccountUser")
		delete(users, user.UserID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func (s *Server) setRoles(w http.ResponseWriter, user *client.AccountUser, roles string, installIDs []string) bool {
	user.Roles = roles
	user.Installs = nil
	if !strings.HasPrefix(roles, "partial") {
		return true
	}
	user.Installs = []client.SiteInstall{}
	for _, id := range installIDs {
		install, ok := s.installs[id]
		if !ok {
			writeError(w, http.StatusBadRequest, "Invalid install.")
			return false
		}
		user.Installs = append(user.Installs, client.SiteInstall{ID: install.ID, Name: install.Name})
	}
	return true
}

func (s *Server) sshKey(w http.ResponseWriter, r *http.Request, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		s.call("listSshKeys")
		writePage(w, r, sorted(s.sshKeys))
	case len(rest) == 0 && r.Method == http.MethodPost:
		s.call("createSshKey")
		var request client.SSHKeyCreate
		if !decode(w, r, "createSshKey", &request) {
			return
		}
		parsed, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(request.PublicKey))
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid public key.")
			return
		}
		key := &client.SSHKey{UUID: s.id(), Fingerprint: ssh.FingerprintSHA256(parsed), Comment: comment, CreatedAt: "2026-01-01T00:00:00Z"}
		s.sshKeys[key.UUID] = key
		writeJSON(w, http.StatusCreated, key)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		s.call("deleteSshKey")
		if _, ok := s.sshKeys[rest[0]]; !ok {
			writeError(w, http.StatusNotFound, "Not Found")
			return
		}
		delete(s.sshKeys, rest[0])
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

// CacheExclusions returns an install's cache exclusions by its name.
func (s *Server) CacheExclusions(installName string) []client.CacheExclusion {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, install := range s.installs {
		if install.Name == installName {
			return append([]client.CacheExclusion{}, s.exclusions[id]...)
		}
	}
	return nil
}
