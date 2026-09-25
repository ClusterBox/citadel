#!/usr/bin/env bash
# Writes the env-file secret to a private temp file, masks every value, runs citadel deploy, and always removes the file (composite actions have no post-step, so cleanup rides on this script's EXIT trap).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

env_name="${CITADEL_ENV:?environment not resolved}"
config="${CITADEL_CONFIG:-citadel.yml}"

cd "${CITADEL_WORKING_DIRECTORY:-.}"

message="${CITADEL_MESSAGE:-}"
if [[ -z "$message" ]]; then
  message="$(git log -1 --format=%s 2>/dev/null || echo deploy) ($(git rev-parse --short HEAD 2>/dev/null || echo unknown))"
fi

args=(deploy)
# Only pass --config when the file exists; otherwise let citadel apply its own
# citadel.yml -> citadel.yaml fallback.
if [[ -f "$config" ]]; then
  args+=(--config "$config")
fi
args+=(--env "$env_name" -m "$message")

if [[ "${CITADEL_SKIP_SECRETS:-false}" == "true" ]]; then
  args+=(--skip-ssm)
else
  if [[ -z "${CITADEL_ENV_FILE_CONTENT:-}" ]]; then
    # shellcheck disable=SC2016
    echo 'citadel: the env-file input is empty; pass env-file: ${{ secrets.CITADEL_ENV_FILE }} or set skip-secrets: true' >&2
    exit 1
  fi
  # mktemp creates the file with mode 0600. No ".env" suffix (the spec's
  # citadel-XXXX.env): macOS mktemp requires the X's to end the template,
  # and citadel does not care about the extension.
  env_file="$(mktemp "${RUNNER_TEMP:?}/citadel-XXXXXX")"
  trap 'rm -f "$env_file"' EXIT
  printf '%s\n' "$CITADEL_ENV_FILE_CONTENT" > "$env_file"
  # Keep the whole .env out of the environment of citadel, cdk, the CDK app,
  # aws and git; they read the private file instead.
  unset CITADEL_ENV_FILE_CONTENT
  mask_env_file "$env_file"
  args+=(--env-file "$env_file")
fi

if [[ "${CITADEL_DEPLOY_INFRA:-false}" == "true" ]]; then
  args+=(--deploy-infra)
fi
if [[ "${CITADEL_WAIT:-true}" == "true" ]]; then
  args+=(--wait)
fi

citadel "${args[@]}"
