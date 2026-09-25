#!/usr/bin/env bash
# Resolves the citadel environment for this run (the 'environment' output).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

env_name="$(resolve_env "${CITADEL_ENVIRONMENT:-}" "${GITHUB_REF_NAME:-}" "${CITADEL_BRANCH_MAP:-}")"
echo "environment=$env_name" >> "$GITHUB_OUTPUT"
echo "citadel: deploying to environment '$env_name'"
