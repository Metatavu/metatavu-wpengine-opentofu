package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/fake/wpengine"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"golang.org/x/crypto/ssh"
)

func TestDomains(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	account := server.AddAccount()
	site := server.AddSite(account, "Acme")
	install := server.AddInstall(account, site, "acmeprod", "production")

	domains := func(secure bool) string {
		return providerBlock(server) + fmt.Sprintf(`
resource "wpengine_domain" "www" {
  install_id      = %q
  name            = "www.example.com"
  primary         = true
  secure_all_urls = %t
}

resource "wpengine_domain" "apex" {
  install_id  = %q
  name        = "example.com"
  redirect_to = wpengine_domain.www.id
}
`, install, secure, install)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: domains(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("wpengine_domain.www", "primary", "true"),
					resource.TestCheckResourceAttr("wpengine_domain.www", "secure_all_urls", "true"),
					resource.TestCheckResourceAttr("wpengine_domain.www", "ownership_status", "TXT_VERIFICATION_PENDING"),
					resource.TestCheckResourceAttr("wpengine_domain.www", "txt_verification_key", "_wpengine.www.example.com"),
					resource.TestCheckResourceAttrPair("wpengine_domain.apex", "redirect_to", "wpengine_domain.www", "id"),
					resource.TestCheckResourceAttr("wpengine_domain.apex", "primary", "false"),
				),
			},
			{
				Config: domains(false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_domain.www", plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if server.Domain("acmeprod", "www.example.com").SecureAllURLs {
						return fmt.Errorf("still secure_all_urls")
					}
					return nil
				},
			},
			{
				ResourceName:      "wpengine_domain.www",
				ImportState:       true,
				ImportStateId:     "acmeprod/www.example.com",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "wpengine_domain.apex",
				ImportState:       true,
				ImportStateId:     install + "/example.com",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if server.Domain("acmeprod", "www.example.com") != nil || server.Domain("acmeprod", "example.com") != nil {
				return fmt.Errorf("not destroyed")
			}
			return nil
		},
	})
}

func TestCacheExclusionsLeaveWPEnginesCookiesAlone(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	account := server.AddAccount()
	site := server.AddSite(account, "Acme")
	install := server.AddInstall(account, site, "acmeprod", "production")
	server.SetCookieExclusion("acmeprod", "wordpress_logged_in")

	exclusions := func(values ...string) string {
		var list []string
		for _, value := range values {
			list = append(list, fmt.Sprintf(`{ type = "url path", value = %q }`, value))
		}
		return providerBlock(server) + fmt.Sprintf(`
resource "wpengine_cache_exclusions" "prod" {
  install_id = %q
  exclusions = [%s, { type = "query arg", value = "^preview$" }]
}
`, install, strings.Join(list, ", "))
	}
	cookies := func() int {
		n := 0
		for _, exclusion := range server.CacheExclusions("acmeprod") {
			if exclusion.Type == "cookie" {
				n++
			}
		}
		return n
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(server) + fmt.Sprintf(`
resource "wpengine_cache_exclusions" "prod" {
  install_id = %q
  exclusions = [{ type = "cookie", value = "session" }]
}
`, install),
				ExpectError: regexp.MustCompile(`Invalid Attribute Value Match`),
			},
			{
				Config: exclusions("^/api/"),
				Check:  resource.TestCheckResourceAttr("wpengine_cache_exclusions.prod", "exclusions.#", "2"),
			},
			{
				Config: exclusions("^/api/", "^/feed/"),
				Check:  resource.TestCheckResourceAttr("wpengine_cache_exclusions.prod", "exclusions.#", "3"),
			},
			{
				ResourceName:      "wpengine_cache_exclusions.prod",
				ImportState:       true,
				ImportStateId:     "acmeprod",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if got := len(server.CacheExclusions("acmeprod")); got != 1 || cookies() != 1 {
				return fmt.Errorf("%d exclusions left, %d cookies", got, cookies())
			}
			return nil
		},
	})
}

func TestAccountUsers(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	account := server.AddAccount()
	site := server.AddSite(account, "Acme")
	install := server.AddInstall(account, site, "acmeprod", "production")

	user := func(roles, installs string) string {
		return providerBlock(server) + fmt.Sprintf(`
resource "wpengine_account_user" "developer" {
  account_id = %q
  email      = "Developer@example.com"
  first_name = "Dee"
  last_name  = "Veloper"
  roles      = %q
  %s
}
`, account, roles, installs)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:      user("partial", ""),
				ExpectError: regexp.MustCompile(`A partial user needs installs`),
			},
			{
				Config:      user("full", fmt.Sprintf(`install_ids = [%q]`, install)),
				ExpectError: regexp.MustCompile(`Only a partial user has installs`),
			},
			{
				Config: user("partial", fmt.Sprintf(`install_ids = [%q]`, install)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("wpengine_account_user.developer", "install_ids.#", "1"),
					resource.TestCheckResourceAttr("wpengine_account_user.developer", "invite_accepted", "false"),
				),
			},
			{
				Config: user("full", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_account_user.developer", plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if got := server.AccountUser(account, "Developer@example.com"); got.Roles != "full" {
						return fmt.Errorf("roles %s", got.Roles)
					}
					return nil
				},
			},
			{
				ResourceName:      "wpengine_account_user.developer",
				ImportState:       true,
				ImportStateId:     account + "/developer@example.com",
				ImportStateVerify: true,
				// The email keeps its declared case; the import has only WP Engine's.
				ImportStateVerifyIgnore: []string{"email"},
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if server.AccountUser(account, "Developer@example.com") != nil {
				return fmt.Errorf("not removed")
			}
			return nil
		},
	})
}

func publicKey(t *testing.T) string {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " someone@example.com"
}

func TestSSHKeys(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	first, second := publicKey(t), publicKey(t)

	key := func(public, extra string) string {
		return providerBlock(server) + extra + fmt.Sprintf(`
resource "wpengine_ssh_key" "someone" {
  public_key = %q
}
`, public)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: key(first, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("wpengine_ssh_key.someone", "fingerprint", regexp.MustCompile(`^SHA256:`)),
					resource.TestCheckResourceAttr("wpengine_ssh_key.someone", "comment", "someone@example.com"),
				),
			},
			{
				// A changed key is a new key.
				Config: key(second, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_ssh_key.someone", plancheck.ResourceActionReplace)},
				},
				Check: func(*terraform.State) error {
					if server.SSHKeys() != 1 {
						return fmt.Errorf("%d keys", server.SSHKeys())
					}
					return nil
				},
			},
			{
				// Forgotten, then imported back by fingerprint: the plan has nothing to replace.
				Config: providerBlock(server) + `
removed {
  from = wpengine_ssh_key.someone
  lifecycle {
    destroy = false
  }
}
`,
			},
			{
				Config: key(second, fmt.Sprintf(`
import {
  to = wpengine_ssh_key.someone
  id = %q
}
`, fingerprint(t, second))),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_ssh_key.someone", plancheck.ResourceActionUpdate)},
				},
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if server.SSHKeys() != 0 {
				return fmt.Errorf("%d keys left", server.SSHKeys())
			}
			return nil
		},
	})
	if server.Calls["createSshKey"] != 2 {
		t.Fatalf("created %d keys", server.Calls["createSshKey"])
	}
}

func fingerprint(t *testing.T, public string) string {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(public))
	if err != nil {
		t.Fatal(err)
	}
	return ssh.FingerprintSHA256(parsed)
}
