#!/usr/bin/env bash
# Reports the CDK app's go.mod (cdk/ next to the config) so the action can set up Go for it.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cdk_dir="${CITADEL_WORKING_DIRECTORY:-.}/$(dirname "${CITADEL_CONFIG:-citadel.yml}")/cdk"
if [[ -f "$cdk_dir/go.mod" ]]; then
  {
    echo "go-mod=$cdk_dir/go.mod"
    if [[ -f "$cdk_dir/go.sum" ]]; then
      echo "go-sum=$cdk_dir/go.sum"
    fi
  } >> "$GITHUB_OUTPUT"
fi
