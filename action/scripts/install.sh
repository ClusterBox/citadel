#!/usr/bin/env bash
# Installs citadel: a checksum-verified release into the tool cache, or a source build for unreleased refs.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

mode="${CITADEL_INSTALL_MODE:?install mode not resolved}"

if [[ "$mode" == "source" ]]; then
  src="$(cd "${GITHUB_ACTION_PATH:?}/.." && pwd)"
  bin_dir="${RUNNER_TEMP:?}/citadel-bin"
  mkdir -p "$bin_dir"
  rev="$(git -C "$src" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  echo "::notice title=citadel::Using an unreleased citadel built from source ($rev). Pin the action to a vX.Y.Z tag for reproducible deploys."
  (cd "$src" && CGO_ENABLED=0 go build -ldflags "-X main.version=source-$rev" -o "$bin_dir/citadel" ./cmd/citadel)
else
  version="${CITADEL_INSTALL_VERSION:?install version not resolved}"
  asset="$(asset_name "$version" "${RUNNER_OS:?}" "${RUNNER_ARCH:?}")"
  bin_dir="${RUNNER_TOOL_CACHE:?}/citadel/$version/$(asset_os_arch "$RUNNER_OS" "$RUNNER_ARCH")"
  if [[ ! -x "$bin_dir/citadel" ]]; then
    base="${CITADEL_RELEASE_BASE_URL:-https://github.com/ClusterBox/citadel/releases/download}/v$version"
    tmp="$(mktemp -d "${RUNNER_TEMP:?}/citadel-dl-XXXXXX")"
    trap 'rm -rf "$tmp"' EXIT
    curl -fsSL --retry 3 -o "$tmp/$asset" "$base/$asset"
    curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$base/checksums.txt"
    verify_checksum "$tmp" "$asset"
    mkdir -p "$tmp/x"
    tar -xzf "$tmp/$asset" -C "$tmp/x" citadel
    mkdir -p "$bin_dir"
    mv "$tmp/x/citadel" "$bin_dir/citadel"
  fi
fi

echo "$bin_dir" >> "${GITHUB_PATH:?}"
"$bin_dir/citadel" --version
