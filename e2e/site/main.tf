# The end-to-end tests' site/: every WordPress resource, against wp-env.
# e2e/run.sh sets the variables and runs the provider it just built.

terraform {
  required_providers {
    wpengine = {
      source = "metatavu.io/metatavu/wpengine"
    }
  }
}

variable "site" {
  type = string
}

variable "releases" {
  description = "Base URL of the local server with the release zips."
  type        = string
}

variable "release_version" {
  type = string
}

variable "release_sha256" {
  type = string
}

variable "description" {
  type    = string
  default = "Managed end to end"
}

variable "active_theme" {
  type    = string
  default = "twentytwentythree"
}

variable "secret" {
  type      = string
  sensitive = true
  default   = "s3cr3t&<x>"
}

provider "wpengine" {
  wordpress_endpoints = { local = var.site }
}

resource "wpengine_wordpress_option" "description" {
  install = "local"
  name    = "blogdescription"
  value   = jsonencode(var.description)
}

resource "wpengine_wordpress_option" "per_page" {
  install = "local"
  name    = "posts_per_page"
  value   = jsonencode(12)
}

resource "wpengine_wordpress_option" "api" {
  install         = "local"
  name            = "acme_api"
  sensitive_value = jsonencode({ url = "https://api.example.com/?a=1&b=2", secret = var.secret, flags = {} })
}

resource "wpengine_wordpress_plugin" "classic_editor" {
  install = "local"
  slug    = "classic-editor"
  source  = "wordpress.org"
}

resource "wpengine_wordpress_plugin" "acme" {
  install = "local"
  slug    = "acme-release"
  source  = "release"
  url     = "${var.releases}/acme-release-${var.release_version}.zip"
  version = var.release_version
  sha256  = var.release_sha256
}

resource "wpengine_wordpress_theme" "tt3" {
  install = "local"
  slug    = "twentytwentythree"
  source  = "wordpress.org"
}

resource "wpengine_wordpress_active_theme" "local" {
  install = "local"
  slug    = var.active_theme

  depends_on = [wpengine_wordpress_theme.tt3]
}
