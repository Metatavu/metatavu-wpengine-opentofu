package provider

import (
	"fmt"
	"regexp"
	"testing"

	fakewp "github.com/Metatavu/metatavu-wpengine-opentofu/internal/fake/wordpress"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func wordpressProvider(server *fakewp.Server, installs ...string) string {
	endpoints := ""
	for _, install := range installs {
		endpoints += fmt.Sprintf("    %s = %q\n", install, server.Endpoint(install))
	}
	return fmt.Sprintf(`
provider "wpengine" {
  wordpress_endpoints = {
%s  }
  wordpress_token_command = %q
}
`, endpoints, fakewp.TokenCommand)
}

func newWordPress(t *testing.T) *fakewp.Server {
	server := fakewp.New()
	t.Cleanup(server.Close)
	server.AddInstall("acmedev", map[string]bool{"blogdescription": false, "posts_per_page": false, "acme_settings": false, "acme_api": true}, "twentytwentyfive", "acme")
	return server
}

func options(server *fakewp.Server, description, secret string) string {
	return wordpressProvider(server, "acmedev") + fmt.Sprintf(`
resource "wpengine_wordpress_option" "description" {
  install = "acmedev"
  name    = "blogdescription"
  value   = jsonencode(%q)
}

resource "wpengine_wordpress_option" "per_page" {
  install = "acmedev"
  name    = "posts_per_page"
  value   = jsonencode(12)
}

resource "wpengine_wordpress_option" "settings" {
  install = "acmedev"
  name    = "acme_settings"
  value   = jsonencode({ mode = "strict", empty = {}, flags = [true, 2] })
}

resource "wpengine_wordpress_option" "api" {
  install         = "acmedev"
  name            = "acme_api"
  sensitive_value = jsonencode({ url = "https://api.example.com/", secret = %q })
}
`, description, secret)
}

func TestWordPressOptions(t *testing.T) {
	server := newWordPress(t)
	writesBeforeImport := 0

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: options(server, "Hello", "s3cr3t"),
				Check: func(*terraform.State) error {
					// Stored as WordPress stores them: a top-level number is a string, {} is [].
					if value, _ := server.Option("acmedev", "posts_per_page"); value != "12" {
						return fmt.Errorf("posts_per_page %#v", value)
					}
					settings, _ := server.Option("acmedev", "acme_settings")
					if fmt.Sprint(settings) != "map[empty:[] flags:[true 2] mode:strict]" {
						return fmt.Errorf("acme_settings %v", settings)
					}
					return nil
				},
			},
			{
				// Changed in wp-admin: the plan shows it, and the apply puts the declared value back.
				PreConfig: func() { server.SetOption("acmedev", "blogdescription", "Changed by hand") },
				Config:    options(server, "Hello", "s3cr3t"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_wordpress_option.description", plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if value, _ := server.Option("acmedev", "blogdescription"); value != "Hello" {
						return fmt.Errorf("blogdescription %v", value)
					}
					return nil
				},
			},
			{
				// A changed secret is detected through its hash, too.
				PreConfig: func() {
					server.SetOption("acmedev", "acme_api", map[string]any{"url": "https://api.example.com/", "secret": "leaked"})
				},
				Config: options(server, "Hello", "s3cr3t"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_wordpress_option.api", plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					value, _ := server.Option("acmedev", "acme_api")
					if fmt.Sprint(value) != "map[secret:s3cr3t url:https://api.example.com/]" {
						return fmt.Errorf("acme_api %v", value)
					}
					return nil
				},
			},
			{
				Config: options(server, "Hello", "rotated"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("wpengine_wordpress_option.api", plancheck.ResourceActionUpdate),
						plancheck.ExpectSensitiveValue("wpengine_wordpress_option.api", tfjsonpath.New("sensitive_value")),
						plancheck.ExpectSensitiveValue("wpengine_wordpress_option.api", tfjsonpath.New("sensitive_value_sha256")),
					},
				},
			},
			{
				// Forgotten, then imported back: the plan has nothing to change but the secret's adoption.
				Config: wordpressProvider(server, "acmedev") + `
removed {
  from = wpengine_wordpress_option.description
  lifecycle { destroy = false }
}
removed {
  from = wpengine_wordpress_option.per_page
  lifecycle { destroy = false }
}
removed {
  from = wpengine_wordpress_option.settings
  lifecycle { destroy = false }
}
removed {
  from = wpengine_wordpress_option.api
  lifecycle { destroy = false }
}
`,
			},
			{
				Config: options(server, "Hello", "rotated") + `
import {
  to = wpengine_wordpress_option.description
  id = "acmedev/blogdescription"
}
import {
  to = wpengine_wordpress_option.per_page
  id = "acmedev/posts_per_page"
}
import {
  to = wpengine_wordpress_option.settings
  id = "acmedev/acme_settings"
}
import {
  to = wpengine_wordpress_option.api
  id = "acmedev/acme_api"
}
`,
				// A value spelled as WordPress stores it imports unchanged; 12 and {} are spelled
				// differently, so they show an update once, and the apply writes nothing.
				PreConfig: func() { writesBeforeImport = server.Writes["acmedev"] },
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("wpengine_wordpress_option.description", plancheck.ResourceActionNoop),
						plancheck.ExpectResourceAction("wpengine_wordpress_option.per_page", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("wpengine_wordpress_option.settings", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(*terraform.State) error {
					if writes := server.Writes["acmedev"] - writesBeforeImport; writes != 0 {
						return fmt.Errorf("the adoption wrote %d times", writes)
					}
					return nil
				},
			},
			{
				Config: options(server, "Hello", "rotated"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestImportingASensitiveOptionWritesNothing(t *testing.T) {
	server := newWordPress(t)
	// Characters JSON encoders escape by default: the provider, the plugin and the fake leave them as they are.
	server.SetOption("acmedev", "acme_api", map[string]any{"url": "https://api.example.com/?a=1&b=<2>", "secret": "s3cr3t"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: wordpressProvider(server, "acmedev") + `
import {
  to = wpengine_wordpress_option.api
  id = "acmedev/acme_api"
}
resource "wpengine_wordpress_option" "api" {
  install         = "acmedev"
  name            = "acme_api"
  sensitive_value = jsonencode({ secret = "s3cr3t", url = "https://api.example.com/?a=1&b=<2>" })
}
`,
			},
		},
	})
	if server.Writes["acmedev"] != 0 {
		t.Fatalf("%d writes", server.Writes["acmedev"])
	}
}

func TestOnePlanReadsAnInstallOnce(t *testing.T) {
	server := newWordPress(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: options(server, "Hello", "s3cr3t")},
			{
				PreConfig: func() { server.StateReads["acmedev"], server.StateReadsByToken["acmedev"] = 0, nil },
				Config:    options(server, "Hello", "s3cr3t"),
				PlanOnly:  true,
				Check: func(*terraform.State) error {
					return nil
				},
			},
		},
	})
	// Four options: each provider process (one OpenTofu command) reads the install once.
	if len(server.StateReadsByToken["acmedev"]) == 0 {
		t.Fatal("no state reads")
	}
	for token, reads := range server.StateReadsByToken["acmedev"] {
		if reads != 1 {
			t.Fatalf("one process read the state %d times (token %.40s…)", reads, token)
		}
	}
}

func TestEveryWriteGetsAFreshToken(t *testing.T) {
	server := newWordPress(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			// Four writes in one apply; the fake refuses a write token used twice.
			{Config: options(server, "Hello", "s3cr3t")},
		},
	})
	if server.Writes["acmedev"] != 4 {
		t.Fatalf("%d writes", server.Writes["acmedev"])
	}
}

func TestWordPressErrors(t *testing.T) {
	server := newWordPress(t)
	t.Cleanup(func() {
		if value, ok := server.Option("acmedev", "blogdescription"); ok {
			t.Errorf("a refused write reached the plugin: %v", value)
		}
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_option" "admin" {
  install = "acmedev"
  name    = "admin_email"
  value   = jsonencode("attacker@example.com")
}
`,
				ExpectError: regexp.MustCompile(`(?s)isn't on the whitelist.*bootstrap/`),
			},
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_option" "both" {
  install         = "acmedev"
  name            = "blogdescription"
  value           = jsonencode("a")
  sensitive_value = jsonencode("b")
}
`,
				ExpectError: regexp.MustCompile(`(?s)Invalid Attribute Combination`),
			},
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_option" "not_json" {
  install = "acmedev"
  name    = "blogdescription"
  value   = "not json"
}
`,
				ExpectError: regexp.MustCompile(`Invalid JSON`),
			},
			{
				// blogdescription is whitelisted as public: the plugin would hand the secret to every read token.
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_option" "secret_in_public" {
  install         = "acmedev"
  name            = "blogdescription"
  sensitive_value = jsonencode("s3cr3t")
}
`,
				ExpectError: regexp.MustCompile(`(?s)isn't sensitive on acmedev.*sensitive = true`),
			},
			{
				// Outside GitHub Actions, with no test token command.
				Config: `
provider "wpengine" {}

resource "wpengine_wordpress_option" "description" {
  install = "acmedev"
  name    = "blogdescription"
  value   = jsonencode("Hello")
}
`,
				ExpectError: regexp.MustCompile(`(?s)site/ is applied only by CI`),
			},
		},
	})
}

func TestWordPressActiveTheme(t *testing.T) {
	server := newWordPress(t)
	theme := func(slug string) string {
		return wordpressProvider(server, "acmedev") + fmt.Sprintf(`
resource "wpengine_wordpress_active_theme" "acmedev" {
  install = "acmedev"
  slug    = %q
}
`, slug)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: theme("acme"),
				Check: func(*terraform.State) error {
					if server.ActiveTheme("acmedev") != "acme" {
						return fmt.Errorf("active theme %s", server.ActiveTheme("acmedev"))
					}
					return nil
				},
			},
			{
				ResourceName:      "wpengine_wordpress_active_theme.acmedev",
				ImportState:       true,
				ImportStateId:     "acmedev",
				ImportStateVerify: true,
			},
			{
				Config:      theme("unknown"),
				ExpectError: regexp.MustCompile(`isn't on the whitelist`),
			},
		},
	})
}

func TestAValueForASensitiveOptionIsRefusedWhenPlanning(t *testing.T) {
	server := newWordPress(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_option" "api" {
  install = "acmedev"
  name    = "acme_api"
  value   = jsonencode({ secret = "in the open" })
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)is sensitive on acmedev.*sensitive_value`),
			},
		},
	})
	if server.Writes["acmedev"] != 0 {
		t.Fatalf("%d writes", server.Writes["acmedev"])
	}
}

// bootstrap/ changes an option's sensitivity, and the configuration follows:
// the plan shows the change, the apply writes nothing (the value is the
// same), and the next plan is empty. Both ways.
func TestAnOptionMovesBetweenPublicAndSensitive(t *testing.T) {
	server := newWordPress(t)
	public := wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_option" "settings" {
  install = "acmedev"
  name    = "acme_settings"
  value   = jsonencode({ mode = "strict" })
}
`
	sensitive := wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_option" "settings" {
  install         = "acmedev"
  name            = "acme_settings"
  sensitive_value = jsonencode({ mode = "strict" })
}
`
	writes := 0
	noWrites := func(*terraform.State) error {
		if server.Writes["acmedev"] != writes {
			return fmt.Errorf("%d writes", server.Writes["acmedev"]-writes)
		}
		return nil
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: public,
				Check:  func(*terraform.State) error { writes = server.Writes["acmedev"]; return nil },
			},
			{
				PreConfig: func() { server.SetSensitive("acmedev", "acme_settings", true) },
				Config:    sensitive,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_wordpress_option.settings", plancheck.ResourceActionUpdate)},
				},
				Check: noWrites,
			},
			{
				Config:           sensitive,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				PreConfig: func() { server.SetSensitive("acmedev", "acme_settings", false) },
				Config:    public,
				Check:     noWrites,
			},
			{
				Config:           public,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}
