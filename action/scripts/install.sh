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
  # A remote action is a tarball checkout with no .git: fall back to the
  # action ref (github.action_ref), else the ref segment of
  # _actions/<owner>/<repo>/<ref>/action, else "unknown".
  if ! rev="$(git -C "$src" rev-parse --short HEAD 2>/dev/null)"; then
    rev="${CITADEL_ACTION_REF:-}"
    if [[ -z "$rev" ]]; then
      rev="$(basename "$(dirname "$GITHUB_ACTION_PATH")")"
    fi
    rev="${rev:-unknown}"
  fi
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
    for file in "$asset" checksums.txt; do
      if ! curl -fsSL --retry 3 -o "$tmp/$file" "$base/$file"; then
        echo "citadel: could not download $base/$file — does release v$version exist (tags without a GitHub Release, e.g. v0.1.0/v0.2.0, cannot be installed) or is it still publishing?" >&2
        exit 1
      fi
    done
    verify_checksum "$tmp" "$asset"
    mkdir -p "$tmp/x"
    tar -xzf "$tmp/$asset" -C "$tmp/x" citadel
    mkdir -p "$bin_dir"
    mv "$tmp/x/citadel" "$bin_dir/citadel"
  fi
fi

echo "$bin_dir" >> "${GITHUB_PATH:?}"
"$bin_dir/citadel" --version
