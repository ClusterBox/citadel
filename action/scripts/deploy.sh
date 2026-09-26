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
# Only omit --config for the default citadel.yml when it doesn't exist, so
# citadel's own citadel.yml -> citadel.yaml fallback applies. Any other
# (non-default) config path is always passed through, so a mistyped path
# fails loudly in citadel instead of silently falling back to ./citadel.yml.
default_config="citadel.yml"
if [[ "$config" != "$default_config" || -f "$config" ]]; then
  args+=(--config "$config")
fi
args+=(--env "$env_name" -m "$message")

# Keep the whole .env out of the environment of citadel, cdk, the CDK app,
# aws and git, also when skip-secrets ignores it; they read the private
# file instead. env_content is a plain (unexported) shell variable.
env_content="${CITADEL_ENV_FILE_CONTENT:-}"
unset CITADEL_ENV_FILE_CONTENT

if [[ "${CITADEL_SKIP_SECRETS:-false}" == "true" ]]; then
  args+=(--skip-ssm)
else
  if [[ -z "$env_content" ]]; then
    # shellcheck disable=SC2016
    echo 'citadel: the env-file input is empty; pass env-file: ${{ secrets.CITADEL_ENV_FILE }} or set skip-secrets: true' >&2
    exit 1
  fi
  # mktemp creates the file with mode 0600. No ".env" suffix (the spec's
  # citadel-XXXX.env): macOS mktemp requires the X's to end the template,
  # and citadel does not care about the extension.
  env_file="$(mktemp "${RUNNER_TEMP:?}/citadel-XXXXXX")"
  trap 'rm -f "$env_file"' EXIT
  printf '%s\n' "$env_content" > "$env_file"
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
