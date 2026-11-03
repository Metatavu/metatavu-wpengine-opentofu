#!/usr/bin/env bash
#
# Installs a release of the wpengine provider into OpenTofu's implicit local
# filesystem mirror, ~/.terraform.d/plugins, where `tofu init` finds the source
# address metatavu.io/metatavu/wpengine without any CLI configuration.
#
# Usage: scripts/install.sh <version>      for example 0.1.0
#
# The release's checksums are verified before installing.

set -euo pipefail

version=${1:?usage: $0 <version>}
version=${version#v}
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in x86_64 | amd64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) echo "Unsupported machine $(uname -m)" >&2; exit 1 ;; esac

name=terraform-provider-wpengine
base=https://github.com/Metatavu/metatavu-wpengine-opentofu/releases/download/v$version
zip=${name}_${version}_${os}_${arch}.zip
target=${TF_PLUGIN_DIR:-$HOME/.terraform.d/plugins}/metatavu.io/metatavu/wpengine/$version/${os}_${arch}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl -fsSL -o "$work/$zip" "$base/$zip"
curl -fsSL -o "$work/SHA256SUMS" "$base/${name}_${version}_SHA256SUMS"
(cd "$work" && grep " $zip\$" SHA256SUMS | shasum -a 256 -c - >/dev/null) || { echo "Checksum mismatch for $zip" >&2; exit 1; }

mkdir -p "$target"
unzip -oq "$work/$zip" -d "$target"
echo "Installed $name $version to $target" >&2
