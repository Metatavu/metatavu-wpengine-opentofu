#!/usr/bin/env bash
#
# End-to-end tests: the provider, run by OpenTofu, against a local WordPress
# (wp-env) with a released IaC mu-plugin and the test token issuer. Packages
# are installed with WP-CLI first, as a project's bootstrap/ does (plugin doc
# D18), and a release update is installed by WordPress's automatic updater
# within the apply (provider D21). Needs Go, OpenTofu, Node (npx), Docker,
# curl, zip and python3; wordpress.org needs network access.
#
# Usage: WP_ENV_PORT=8888 e2e/run.sh
#
# E2E_PLUGIN_ZIP runs against a locally built plugin zip instead of the
# pinned release, to test a plugin change before it's released.

set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

plugin_version=0.3.0
plugin_sha256=bb85202feb87dfa83f8cf72aa7495ee44ab3beb39cf62fd751b6bc7a133fc899
port=${WP_ENV_PORT:-8888}
releases_port=${E2E_RELEASES_PORT:-8897}
site=http://localhost:$port
build=$root/e2e/build
failures=0

check() { if [[ $2 == "$3" ]]; then echo "ok   $1"; else echo "FAIL $1: got '$2', expected '$3'" >&2; failures=$((failures + 1)); fi; }
# WP-CLI in the site's container: its exit status, and its output without wp-env's own lines.
wp() {
	local out status=0
	out=$(cd e2e && npx -y @wordpress/env@10 run cli wp "$@" 2>/dev/null) || status=$?
	printf '%s\n' "$out" | grep -v -E '^(ℹ|✔)' | tr -d '\r' | sed '/^$/d' || true
	return "$status"
}

# A running wp-env keeps its mounts of the old build, so it starts again after the build is replaced.
(cd e2e && npx -y @wordpress/env@10 stop >/dev/null 2>&1) || true
rm -rf "$build" e2e/site/.terraform* e2e/site/terraform.tfstate*
mkdir -p "$build/bin" "$build/releases"

echo "== the IaC mu-plugin ${E2E_PLUGIN_ZIP:-v$plugin_version}"
zip=$build/metatavu-wordpress-iac-$plugin_version.zip
url=https://github.com/Metatavu/metatavu-wordpress-iac/releases/download/v$plugin_version/metatavu-wordpress-iac-$plugin_version.zip
if [[ -n ${E2E_PLUGIN_ZIP:-} ]]; then
	cp "$E2E_PLUGIN_ZIP" "$zip"
	plugin_sha256=$(shasum -a 256 "$zip" | cut -d' ' -f1)
elif ! curl -fsSL --retry 2 -o "$zip" "$url"; then
	asset=$(curl -fsSL "https://api.github.com/repos/Metatavu/metatavu-wordpress-iac/releases/tags/v$plugin_version" |
		python3 -I -c 'import json, sys; print(next(a["url"] for a in json.load(sys.stdin)["assets"] if a["name"] == sys.argv[1]))' "$(basename "$zip")")
	curl -fsSL --retry 2 -H 'Accept: application/octet-stream' -o "$zip" "$asset"
fi
echo "$plugin_sha256  $zip" | shasum -a 256 -c - >/dev/null
unzip -q "$zip" -d "$build/plugin"

echo "== the provider, and the token issuer"
go build -o "$build/bin/terraform-provider-wpengine" .
go build -o "$build/bin/token" ./e2e/token
export IAC_E2E_KEY=$build/issuer-key.pem
"$build/bin/token" config "$site" e2e/whitelist.json >"$build/plugin/metatavu-wordpress-iac/config.php"
export WPENGINE_WORDPRESS_TOKEN_COMMAND="$build/bin/token token"
cat >"$build/tofurc" <<TOFURC
provider_installation {
  dev_overrides {
    "metatavu.io/metatavu/wpengine" = "$build/bin"
  }
  direct {}
}
TOFURC
export TF_CLI_CONFIG_FILE=$build/tofurc

echo "== release zips, served locally"
for version in 0.9.0 1.0.0 1.1.0; do
	mkdir -p "$build/release-$version/acme-release"
	printf '<?php\n/**\n * Plugin Name: Acme Release\n * Version: %s\n */\n' "$version" >"$build/release-$version/acme-release/acme-release.php"
	(cd "$build/release-$version" && zip -qrX "$build/releases/acme-release-$version.zip" acme-release)
done
sha() { shasum -a 256 "$build/releases/acme-release-$1.zip" | cut -d' ' -f1; }
python3 -I -m http.server "$releases_port" --bind 127.0.0.1 --directory "$build/releases" >/dev/null 2>&1 &
server=$!
trap 'kill $server 2>/dev/null || true' EXIT

echo "== WordPress"
(cd e2e && npx -y @wordpress/env@10 start >/dev/null)

tofu_run() {
	local command=$1 version=$2
	shift 2
	tofu -chdir=e2e/site "$command" -input=false -no-color -var "site=$site" -var "releases=http://127.0.0.1:$releases_port" \
		-var "release_version=$version" -var "release_sha256=$(sha "$version")" "$@"
}
# The plan's exit code: 0 no changes, 2 changes. A plan with changes is shown when it wasn't expected.
plan_exit() {
	local code=0
	tofu_run plan "$@" -detailed-exitcode >"$build/plan.txt" 2>&1 || code=$?
	echo "$code"
}
expect_no_changes() {
	local code
	code=$(plan_exit "$2")
	check "$1" "$code" 0
	[[ $code == 0 ]] || grep -E '^\s+[~+-]|will be|must be' "$build/plan.txt" >&2 || true
}
# "active", "inactive" or "deleted" from a successful list; anything else when WP-CLI fails.
plugin_status() {
	local list
	list=$(wp plugin list --fields=name,status --format=csv) || { echo "wp-cli failed"; return; }
	awk -F, -v slug="$1" '$1 == slug { print $2; found = 1 } END { if (!found) print "deleted" }' <<<"$list"
}
theme_status() {
	local list
	list=$(wp theme list --fields=name,status --format=csv) || { echo "wp-cli failed"; return; }
	awk -F, -v slug="$1" '$1 == slug { print $2; found = 1 } END { if (!found) print "deleted" }' <<<"$list"
}

echo "== packages installed as bootstrap/ does"
# Destroying leaves packages installed (provider D20), so earlier runs' are removed first.
wp theme activate twentytwentyfive >/dev/null || true
wp plugin deactivate classic-editor acme-release >/dev/null 2>&1 || true
wp plugin delete classic-editor acme-release >/dev/null 2>&1 || true
wp theme delete twentytwentythree >/dev/null 2>&1 || true
wp option delete metatavu_iac_release_plugin_acme-release >/dev/null 2>&1 || true
wp plugin install classic-editor >/dev/null
wp theme install twentytwentythree >/dev/null
# The CLI container sees the site's files, not this machine's, so the zip goes through wp-content.
wp eval "file_put_contents( WP_CONTENT_DIR . '/e2e-package.zip', base64_decode( '$(base64 <"$build/releases/acme-release-0.9.0.zip" | tr -d '\n')' ) );" >/dev/null
wp plugin install wp-content/e2e-package.zip >/dev/null
wp eval "unlink( WP_CONTENT_DIR . '/e2e-package.zip' );" >/dev/null
check "the release is installed at its first version, inactive" "$(wp plugin get acme-release --field=version) $(plugin_status acme-release)" "0.9.0 inactive"

echo "== apply"
tofu_run apply 1.0.0 -auto-approve >/dev/null
expect_no_changes "the next plan has no changes" 1.0.0
check "an option" "$(wp option get blogdescription)" "Managed end to end"
check "a number is stored as WordPress stores it" "$(wp option get posts_per_page)" 12
check "the secret's URL keeps its & and its {} is []" "$(wp option get acme_api --format=json)" '{"flags":[],"secret":"s3cr3t&<x>","url":"https:\/\/api.example.com\/?a=1&b=2"}'
check "a wordpress.org plugin, active" "$(plugin_status classic-editor)" active
check "a release plugin, updated by WordPress within the apply, and active" "$(wp plugin get acme-release --field=version) $(plugin_status acme-release)" "1.0.0 active"
check "the active theme" "$(wp option get stylesheet)" twentytwentythree

echo "== drift made in wp-admin is shown and put back"
wp option update blogdescription "Changed by hand" >/dev/null
wp plugin deactivate classic-editor >/dev/null
wp option update acme_api '{"secret":"leaked"}' --format=json >/dev/null
check "the plan shows the drift" "$(plan_exit 1.0.0)" 2
tofu_run apply 1.0.0 -auto-approve >/dev/null
check "the option is put back" "$(wp option get blogdescription)" "Managed end to end"
check "the plugin is active again" "$(plugin_status classic-editor)" active
check "the secret is put back" "$(wp option get acme_api --format=json | python3 -I -c 'import json, sys; print(json.load(sys.stdin)["secret"])')" "s3cr3t&<x>"
expect_no_changes "then there are no changes" 1.0.0

echo "== a new release"
tofu_run apply 1.1.0 -auto-approve >/dev/null
check "the release is updated" "$(wp plugin get acme-release --field=version)" 1.1.0
expect_no_changes "then there are no changes" 1.1.0

echo "== the plugin logged the writes and the changes"
writes=$(wp db query "SELECT COUNT(*) FROM wp_metatavu_iac_log WHERE kind = 'write'" --skip-column-names | tr -d '[:space:]')
changes=$(wp db query "SELECT COUNT(*) FROM wp_metatavu_iac_log WHERE kind = 'change' AND target = 'option:blogdescription'" --skip-column-names | tr -d '[:space:]')
check "writes are logged" "$([[ ${writes:-0} -gt 0 ]] && echo yes)" yes
check "the change in wp-admin is logged" "$([[ ${changes:-0} -gt 0 ]] && echo yes)" yes

echo "== destroy"
tofu_run apply 1.1.0 -auto-approve -var active_theme=twentytwentyfive >/dev/null
tofu_run destroy 1.1.0 -auto-approve -var active_theme=twentytwentyfive >/dev/null
check "the wordpress.org plugin is deactivated and left for bootstrap/" "$(plugin_status classic-editor)" inactive
check "the release plugin is deactivated and left for bootstrap/" "$(plugin_status acme-release)" inactive
check "the theme is left for bootstrap/" "$(theme_status twentytwentythree)" inactive
check "the option is left in WordPress" "$(wp option get blogdescription)" "Managed end to end"

if ((failures > 0)); then
	echo "$failures failed" >&2
	exit 1
fi
echo "all passed"
