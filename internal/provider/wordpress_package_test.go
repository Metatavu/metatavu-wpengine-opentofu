package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	fakewp "github.com/Metatavu/metatavu-wpengine-opentofu/internal/fake/wordpress"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// releases serves zips at /<name>.zip, and their SHA-256s.
func releases(t *testing.T, names ...string) (*httptest.Server, map[string]string) {
	sums := map[string]string{}
	for _, name := range names {
		s := sha256.Sum256([]byte("zip of " + name))
		sums[name] = hex.EncodeToString(s[:])
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "zip of "+strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".zip"))
	}))
	t.Cleanup(server.Close)
	return server, sums
}

// shortReleaseWait makes an update's apply poll the fake WP-Cron quickly.
func shortReleaseWait(t *testing.T, timeout time.Duration) {
	saved := releaseWait
	releaseWait.interval, releaseWait.timeout = 10*time.Millisecond, timeout
	t.Cleanup(func() { releaseWait = saved })
}

func acmeRelease(server *fakewp.Server, zips string, sums map[string]string, version string) string {
	return wordpressProvider(server, "acmedev") + fmt.Sprintf(`
resource "wpengine_wordpress_plugin" "acme" {
  install = "acmedev"
  slug    = "acme-release"
  source  = "release"
  url     = "%s/acme-%s.zip"
  version = %q
  sha256  = %q
}
`, zips, version, version, sums["acme-"+version])
}

func TestWordPressPlugins(t *testing.T) {
	server := newWordPress(t)
	server.Whitelist("acmedev", "plugins", "classic-editor", "acme-release")
	// Installed by the project's bootstrap/, inactive (plugin D18).
	server.Install("acmedev", "plugins", "classic-editor", "1.7.0")
	server.Install("acmedev", "plugins", "acme-release", "0.9.0")
	zips, sums := releases(t, "acme-1.0.0", "acme-1.1.0")
	shortReleaseWait(t, 5*time.Second)

	plugins := func(active bool, version string) string {
		return wordpressProvider(server, "acmedev") + fmt.Sprintf(`
resource "wpengine_wordpress_plugin" "classic_editor" {
  install = "acmedev"
  slug    = "classic-editor"
  source  = "wordpress.org"
  active  = %t
}

resource "wpengine_wordpress_plugin" "acme" {
  install = "acmedev"
  slug    = "acme-release"
  source  = "release"
  url     = "%s/acme-%s.zip"
  version = %q
  sha256  = %q
}
`, active, zips.URL, version, version, sums["acme-"+version])
	}
	uploads := 0

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				// Taken over: the release's update is installed by WP-Cron within the apply.
				Config: plugins(true, "1.0.0"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("wpengine_wordpress_plugin.classic_editor", "installed_version", "1.7.0"),
					resource.TestCheckResourceAttr("wpengine_wordpress_plugin.acme", "installed_version", "1.0.0"),
					func(*terraform.State) error {
						if p := server.Package("acmedev", "plugins", "classic-editor"); !p.Active {
							return fmt.Errorf("classic-editor %+v", p)
						}
						if p := server.Package("acmedev", "plugins", "acme-release"); !p.Active || p.Version != "1.0.0" || p.Pending != "" {
							return fmt.Errorf("acme-release %+v", p)
						}
						if server.CronRuns["acmedev"] == 0 {
							return fmt.Errorf("WP-Cron wasn't run")
						}
						return nil
					},
				),
			},
			{
				Config: plugins(false, "1.1.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("wpengine_wordpress_plugin.classic_editor", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("wpengine_wordpress_plugin.acme", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(*terraform.State) error {
					if p := server.Package("acmedev", "plugins", "classic-editor"); p.Active {
						return fmt.Errorf("classic-editor still active")
					}
					if p := server.Package("acmedev", "plugins", "acme-release"); p.Version != "1.1.0" {
						return fmt.Errorf("acme-release %+v", p)
					}
					return nil
				},
			},
			{
				// Changed in wp-admin: the plan shows it, and the apply puts the declared release back.
				PreConfig: func() {
					server.SetPackage("acmedev", "plugins", "acme-release", fakewp.Package{Installed: true, Version: "0.9.0", Active: true})
				},
				Config: plugins(false, "1.1.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("wpengine_wordpress_plugin.acme", plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if p := server.Package("acmedev", "plugins", "acme-release"); p.Version != "1.1.0" {
						return fmt.Errorf("acme-release %+v", p)
					}
					uploads = server.Uploads["acmedev"]
					return nil
				},
			},
			{
				Config: wordpressProvider(server, "acmedev") + `
removed {
  from = wpengine_wordpress_plugin.classic_editor
  lifecycle { destroy = false }
}
removed {
  from = wpengine_wordpress_plugin.acme
  lifecycle { destroy = false }
}
`,
			},
			{
				// Imported back: the release installed at the declared version isn't uploaded again.
				Config: plugins(false, "1.1.0") + `
import {
  to = wpengine_wordpress_plugin.classic_editor
  id = "acmedev/classic-editor"
}
import {
  to = wpengine_wordpress_plugin.acme
  id = "acmedev/acme-release"
}
`,
				Check: func(*terraform.State) error {
					if server.Uploads["acmedev"] != uploads {
						return fmt.Errorf("the import uploaded %d times", server.Uploads["acmedev"]-uploads)
					}
					return nil
				},
			},
			{
				Config: plugins(false, "1.1.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
		// Destroying deactivates and leaves the plugins installed: bootstrap/ removes them (provider D20).
		CheckDestroy: func(*terraform.State) error {
			for _, slug := range []string{"classic-editor", "acme-release"} {
				if p := server.Package("acmedev", "plugins", slug); !p.Installed || p.Active {
					return fmt.Errorf("%s %+v", slug, p)
				}
			}
			return nil
		},
	})
}

func TestAPackageMustBeInstalledByBootstrapFirst(t *testing.T) {
	server := newWordPress(t)
	server.Whitelist("acmedev", "plugins", "classic-editor")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_plugin" "classic_editor" {
  install = "acmedev"
  slug    = "classic-editor"
  source  = "wordpress.org"
}
`,
				ExpectError: regexp.MustCompile(`(?s)install\s+it\s+with\s+the\s+project's\s+bootstrap/\s+first`),
			},
		},
	})
}

func TestAFailedUpdateFailsTheApply(t *testing.T) {
	server := newWordPress(t)
	server.Whitelist("acmedev", "plugins", "acme-release")
	server.Install("acmedev", "plugins", "acme-release", "1.0.0")
	server.FailUpdates["acme-release"] = "Could not copy file."
	zips, sums := releases(t, "acme-1.1.0")
	shortReleaseWait(t, 5*time.Second)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:      acmeRelease(server, zips.URL, sums, "1.1.0"),
				ExpectError: regexp.MustCompile(`(?s)automatic\s+updater\s+failed:\s+Could\s+not\s+copy\s+file\.`),
			},
		},
	})
	if p := server.Package("acmedev", "plugins", "acme-release"); p.Version != "1.0.0" || p.Pending != "1.1.0" {
		t.Fatalf("acme-release %+v", p)
	}
}

func TestAnUpdateWordPressDoesNotInstallTimesOut(t *testing.T) {
	server := newWordPress(t)
	server.Whitelist("acmedev", "plugins", "acme-release")
	server.Install("acmedev", "plugins", "acme-release", "1.0.0")
	server.HoldUpdates = true
	zips, sums := releases(t, "acme-1.1.0")
	shortReleaseWait(t, 100*time.Millisecond)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:      acmeRelease(server, zips.URL, sums, "1.1.0"),
				ExpectError: regexp.MustCompile(`(?s)still\s+pending\s+after`),
			},
		},
	})
}

func TestAReleaseWithTheWrongChecksumIsNotUploaded(t *testing.T) {
	server := newWordPress(t)
	server.Whitelist("acmedev", "plugins", "acme-release")
	server.Install("acmedev", "plugins", "acme-release", "0.9.0")
	zips, sums := releases(t, "acme-1.0.0", "other")
	sums["acme-1.0.0"] = sums["other"]

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:      acmeRelease(server, zips.URL, sums, "1.0.0"),
				ExpectError: regexp.MustCompile(`(?s)not\s+the\s+declared`),
			},
		},
	})
	if server.Uploads["acmedev"] != 0 {
		t.Fatalf("%d uploads", server.Uploads["acmedev"])
	}
}

func TestWordPressThemes(t *testing.T) {
	server := newWordPress(t)
	server.Whitelist("acmedev", "themes", "twentytwentythree")
	server.Install("acmedev", "themes", "twentytwentythree", "1.7")
	theme := func(active string) string {
		return wordpressProvider(server, "acmedev") + fmt.Sprintf(`
resource "wpengine_wordpress_theme" "tt3" {
  install = "acmedev"
  slug    = "twentytwentythree"
  source  = "wordpress.org"
}

resource "wpengine_wordpress_active_theme" "acmedev" {
  install = "acmedev"
  slug    = %q

  depends_on = [wpengine_wordpress_theme.tt3]
}
`, active)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: theme("twentytwentythree"),
				Check:  resource.TestCheckResourceAttr("wpengine_wordpress_theme.tt3", "installed_version", "1.7"),
			},
			{Config: theme("twentytwentyfive")},
			{
				// Removed from code while installed: it's left installed for bootstrap/ to remove.
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_active_theme" "acmedev" {
  install = "acmedev"
  slug    = "twentytwentyfive"
}
`,
				Check: func(*terraform.State) error {
					if !server.Package("acmedev", "themes", "twentytwentythree").Installed {
						return fmt.Errorf("twentytwentythree was deleted")
					}
					return nil
				},
			},
		},
	})
}

func TestWordPressPackageValidation(t *testing.T) {
	server := newWordPress(t)
	server.Whitelist("acmedev", "plugins", "classic-editor")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_plugin" "release" {
  install = "acmedev"
  slug    = "classic-editor"
  source  = "release"
}
`,
				ExpectError: regexp.MustCompile(`A release needs url`),
			},
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_plugin" "org" {
  install = "acmedev"
  slug    = "classic-editor"
  source  = "wordpress.org"
  version = "1.0"
}
`,
				ExpectError: regexp.MustCompile(`Only a release has version`),
			},
			{
				Config: wordpressProvider(server, "acmedev") + `
resource "wpengine_wordpress_plugin" "unlisted" {
  install = "acmedev"
  slug    = "akismet"
  source  = "wordpress.org"
}
`,
				ExpectError: regexp.MustCompile(`(?s)whitelist.*bootstrap/`),
			},
		},
	})
}
