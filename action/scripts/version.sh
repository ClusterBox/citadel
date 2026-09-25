#!/usr/bin/env bash
# Decides whether to install a released citadel or build it from source.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

resolved="$(resolve_version "${CITADEL_VERSION_INPUT:-}" "${CITADEL_ACTION_REF:-}" "${GITHUB_ACTION_PATH:-}")"
read -r mode version <<< "$resolved"
{
  echo "mode=$mode"
  echo "version=${version:-}"
} >> "$GITHUB_OUTPUT"
