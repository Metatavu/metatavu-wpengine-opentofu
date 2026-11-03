package client_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/fake/wpengine"
)

func TestListFollowsPages(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	account := server.AddAccount()
	site := server.AddSite(account, "Acme")
	for i := 0; i < 230; i++ {
		server.AddInstall(account, site, fmt.Sprintf("acme%03d", i), "development")
	}
	c, err := client.New(server.URL, wpengine.Username, wpengine.Password, nil)
	if err != nil {
		t.Fatal(err)
	}
	installs, err := c.ListInstalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(installs) != 230 || server.Calls["listInstalls"] != 3 {
		t.Fatalf("got %d installs in %d requests", len(installs), server.Calls["listInstalls"])
	}
}

func TestRetriesWhenRateLimited(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"id": "s1", "name": "Acme", "account": {"id": "a1"}}`)
	}))
	defer server.Close()
	c, _ := client.New(server.URL, "user", "password", nil)
	site, err := c.GetSite(context.Background(), "s1")
	if err != nil || site.Name != "Acme" || attempts != 3 {
		t.Fatalf("site %v, err %v, attempts %d", site, err, attempts)
	}
}

func TestGivesUpAfterRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"message": "Service Unavailable"}`)
	}))
	defer server.Close()
	c, _ := client.New(server.URL, "user", "password", nil)
	c.Retries, c.RetryWait = 1, time.Millisecond
	if _, err := c.GetSite(context.Background(), "s1"); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
		t.Fatalf("err %v", err)
	}
}

func TestErrorsNameTheirFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"message": "Invalid request.", "errors": [{"resource": "Install", "field": "name", "type": "invalid_value", "code": "taken", "message": "is already taken"}]}`)
	}))
	defer server.Close()
	c, _ := client.New(server.URL, "user", "password", nil)
	_, err := c.CreateInstall(context.Background(), client.InstallCreate{Name: "acme", AccountID: "a1"})
	if err == nil || err.Error() != "Invalid request.; name: is already taken" {
		t.Fatalf("err %v", err)
	}
}

func TestFakeRefusesFieldsTheAPIDoesNotHave(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/sites", strings.NewReader(`{"name": "Acme", "account_id": "x", "php_version": "8.4"}`))
	request.SetBasicAuth(wpengine.Username, wpengine.Password)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestNeedsBothCredentials(t *testing.T) {
	if _, err := client.New(client.DefaultURL, "user", "", nil); err == nil {
		t.Fatal("no error")
	}
}
