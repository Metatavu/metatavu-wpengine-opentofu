# metatavu-wpengine-opentofu

OpenTofu provider for WP Engine hosting and the WordPress configuration behind it: `wpengine_*` resources through the [WP Engine API](https://wpengineapi.com/reference), and `wpengine_wordpress_*` resources through [metatavu-wordpress-iac](https://github.com/Metatavu/metatavu-wordpress-iac).

Work in progress: the platform resources below are in place; the WordPress resources follow.

## Installing

The provider isn't in a public registry. Its source address is `metatavu.io/metatavu/wpengine`, and it's installed from a filesystem mirror:

```sh
scripts/install.sh 0.1.0
```

That puts the release, after checking its checksums, into OpenTofu's implicit local mirror `~/.terraform.d/plugins`, where `tofu init` finds it without any CLI configuration. In GitHub Actions:

```yaml
- uses: Metatavu/metatavu-wpengine-opentofu/.github/actions/install@v0.1.0
  with:
    version: 0.1.0
```

```hcl
terraform {
  required_providers {
    wpengine = {
      source  = "metatavu.io/metatavu/wpengine"
      version = "~> 0.1"
    }
  }
}
```

The lock file records the hashes of the platforms that ran `tofu init`. To record others, run `tofu providers lock -fs-mirror=$HOME/.terraform.d/plugins -platform=linux_amd64 -platform=darwin_arm64` with each platform's release installed.

## Configuration

Each setting can also come from its environment variable.

| Setting | Variable | For |
|---|---|---|
| `api_username`, `api_password` | `WPENGINE_API_USERNAME`, `WPENGINE_API_PASSWORD` | The `wpengine_*` platform resources only. An API user from the portal's API Access page |
| `api_url` | `WPENGINE_API_URL` | Tests. Defaults to `https://api.wpengineapi.com/v1` |

The platform credentials are used only from a laptop, in a project's `bootstrap/`. A configuration without them works until it uses a platform resource.

## Resources

| Resource | What it is | Changes in place | Replaces |
|---|---|---|---|
| `wpengine_site` | A site: the group of a project's installs. Destroying one is refused while it has installs, because WP Engine deletes them with it | `name` | `account_id` |
| `wpengine_install` | One WordPress environment of a site | `site_id`, `environment` | `account_id`, `name` |
| `wpengine_domain` | A domain or a redirect on an install. It serves traffic once its TXT record (`txt_verification_key`, `txt_verification_value`) verifies its ownership. WP Engine manages its certificate (`ssl_status`) | `primary`, `redirect_to`, `secure_all_urls` | `install_id`, `name`; removing `redirect_to` |
| `wpengine_cache_exclusions` | Every cache exclusion of an install (`url path`, `query arg`). Cookie exclusions are WP Engine's own and are left alone. Destroying it removes the declared ones | `exclusions` | `install_id` |
| `wpengine_account_user` | A person's access to the account. Creating one emails them an invitation. A `partial` user reaches only its `install_ids` | `roles`, `install_ids` | `account_id`, `email`, `first_name`, `last_name` |
| `wpengine_ssh_key` | An SSH public key of the API user, for WP Engine's SSH gateway | | `public_key` |

An install's `php_version` is declared but can't be applied: the WP Engine API can read it, not change it. A difference from the live version shows in the plan, and apply refuses until the version is changed in the portal. A new install that WP Engine creates with another version gets a warning, and the next plan shows the difference.

```hcl
resource "wpengine_site" "acme" {
  account_id = var.wpengine_account_id
  name       = "Acme"
}

resource "wpengine_install" "development" {
  account_id  = var.wpengine_account_id
  site_id     = wpengine_site.acme.id
  name        = "acmedev"
  environment = "development"
  php_version = "8.4"
}
```

Imports: a site or an install by its ID or name; a domain as `<install>/<domain>` (each by ID or name); cache exclusions by the install's ID or name; an account user as `<account_id>/<email>` or `<account_id>/<user_id>`; an SSH key by its ID or fingerprint. An imported SSH key has no public key in state, because the API doesn't return it: the first plan after importing updates it in place when the declared key has the same fingerprint.

```hcl
import {
  to = wpengine_install.development
  id = "acmedev"
}
```

## Development

```sh
go vet ./...
go test ./...
```

The resource tests run OpenTofu (`tofu` on the `PATH`) against an in-memory stand-in for the WP Engine API (`internal/fake/wpengine`), which refuses request fields that aren't in the API's OpenAPI document. `internal/openapi/fields.go` lists those fields; regenerate it from the live document with `go generate ./internal/openapi`, and the tests then show any field the provider uses that the API no longer has.

Releases are built by goreleaser from a `v*` tag on `main`.
