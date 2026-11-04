package provider

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/fake/wpengine"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The tests run OpenTofu against the fake WP Engine API. OpenTofu's default
// registry is registry.opentofu.org, so the test provider is attached there.
func TestMain(m *testing.M) {
	if os.Getenv("TF_ACC_TERRAFORM_PATH") == "" {
		if tofu, err := exec.LookPath("tofu"); err == nil {
			os.Setenv("TF_ACC_TERRAFORM_PATH", tofu)
		}
	}
	if os.Getenv("TF_ACC_PROVIDER_HOST") == "" {
		os.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
	}
	for _, variable := range []string{"WPENGINE_API_URL", "WPENGINE_API_USERNAME", "WPENGINE_API_PASSWORD", "WPENGINE_WORDPRESS_TOKEN_COMMAND", "WPENGINE_RELEASE_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"} {
		os.Unsetenv(variable)
	}
	installPollInterval = 10 * time.Millisecond
	os.Exit(m.Run())
}

var factories = map[string]func() (tfprotov6.ProviderServer, error){
	"wpengine": providerserver.NewProtocol6WithError(New("test")()),
}

func providerBlock(server *wpengine.Server) string {
	return fmt.Sprintf(`
provider "wpengine" {
  api_url      = %q
  api_username = %q
  api_password = %q
}
`, server.URL, wpengine.Username, wpengine.Password)
}

func siteAndInstall(server *wpengine.Server, account, siteName, environment, php string) string {
	return providerBlock(server) + fmt.Sprintf(`
resource "wpengine_site" "acme" {
  account_id = %q
  name       = %q
}

resource "wpengine_install" "dev" {
  account_id  = %q
  site_id     = wpengine_site.acme.id
  name        = "acmedev"
  environment = %q
  php_version = %q
}
`, account, siteName, account, environment, php)
}

func TestSiteAndInstall(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	server.PendingReads = 2
	account := server.AddAccount()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: siteAndInstall(server, account, "Acme", "development", "8.4"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("wpengine_install.dev", "site_id", "wpengine_site.acme", "id"),
					resource.TestCheckResourceAttr("wpengine_install.dev", "status", "active"),
					resource.TestCheckResourceAttr("wpengine_install.dev", "cname", "acmedev.wpengine.com"),
					resource.TestCheckResourceAttr("wpengine_install.dev", "php_version", "8.4"),
					resource.TestCheckResourceAttr("wpengine_install.dev", "is_multisite", "false"),
				),
			},
			{
				// The site's name and the install's environment type change in place.
				Config: siteAndInstall(server, account, "Acme website", "staging", "8.4"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("wpengine_site.acme", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("wpengine_install.dev", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(*terraform.State) error {
					if got := server.Install("acmedev"); *got.Environment != "staging" {
						return fmt.Errorf("environment %s", *got.Environment)
					}
					if server.Site("Acme website") == nil {
						return fmt.Errorf("site wasn't renamed")
					}
					return nil
				},
			},
			{
				ResourceName:      "wpengine_site.acme",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "wpengine_install.dev",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// By name, as a project taking over a site imports them.
				ResourceName:      "wpengine_install.dev",
				ImportState:       true,
				ImportStateId:     "acmedev",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "wpengine_site.acme",
				ImportState:       true,
				ImportStateId:     "Acme website",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if server.Install("acmedev") != nil || server.Site("Acme website") != nil {
				return fmt.Errorf("not destroyed")
			}
			return nil
		},
	})
}

func TestPHPVersionIsDeclaredButNotApplied(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	account := server.AddAccount()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: siteAndInstall(server, account, "Acme", "development", wpengine.DefaultPHPVersion),
			},
			{
				// Someone changed PHP in the portal: the plan shows it, and apply refuses.
				PreConfig:   func() { server.SetPHPVersion("acmedev", "8.3") },
				Config:      siteAndInstall(server, account, "Acme", "development", wpengine.DefaultPHPVersion),
				ExpectError: regexp.MustCompile(`(?s)runs PHP 8\.3\..*change it to\s+8\.4\s+in\s+the\s+WP\s+Engine\s+portal`),
			},
			{
				// Declaring what's live makes the plan empty again.
				Config:   siteAndInstall(server, account, "Acme", "development", "8.3"),
				PlanOnly: true,
			},
			{
				// Once the portal matches the declared version, nothing is left to do.
				PreConfig: func() { server.SetPHPVersion("acmedev", wpengine.DefaultPHPVersion) },
				Config:    siteAndInstall(server, account, "Acme", "development", wpengine.DefaultPHPVersion),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
	if server.Calls["updateInstall"] != 0 {
		t.Fatalf("updateInstall called %d times", server.Calls["updateInstall"])
	}
}

func TestNewInstallWithAnotherPHPVersionShowsInTheNextPlan(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	account := server.AddAccount()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:             siteAndInstall(server, account, "Acme", "development", "8.3"),
				ExpectNonEmptyPlan: true,
				Check:              resource.TestCheckResourceAttr("wpengine_install.dev", "php_version", "8.3"),
			},
		},
	})
}

func TestSiteWithInstallsIsNotDeleted(t *testing.T) {
	server := wpengine.New()
	defer server.Close()
	account := server.AddAccount()
	site := server.AddSite(account, "Acme")
	server.AddInstall(account, site, "acmeprod", "production")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(server) + fmt.Sprintf(`
import {
  to = wpengine_site.acme
  id = "Acme"
}

resource "wpengine_site" "acme" {
  account_id = %q
  name       = "Acme"
}
`, account),
			},
			{
				Config:      providerBlock(server),
				ExpectError: regexp.MustCompile(`(?s)still has installs.*acmeprod`),
			},
			{
				Config: providerBlock(server) + `
removed {
  from = wpengine_site.acme
  lifecycle {
    destroy = false
  }
}
`,
			},
		},
	})
	if server.Site("Acme") == nil || server.Install("acmeprod") == nil {
		t.Fatal("the site or its install was deleted")
	}
}

func TestPlatformResourcesNeedCredentials(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "wpengine" {}

resource "wpengine_site" "acme" {
  account_id = "00000000-0000-4000-8000-000000000001"
  name       = "Acme"
}
`,
				ExpectError: regexp.MustCompile(`No WP Engine API credentials`),
			},
			{
				Config: `
provider "wpengine" {
  api_username = "someone"
}

resource "wpengine_site" "acme" {
  account_id = "00000000-0000-4000-8000-000000000001"
  name       = "Acme"
}
`,
				ExpectError: regexp.MustCompile(`Incomplete WP Engine API credentials`),
			},
		},
	})
}
