#!/usr/bin/env bash
# Finds the run citadel just recorded, for the artifact upload and job summary. Runs even when the deploy failed.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

citadel_dir="${CITADEL_WORKING_DIRECTORY:-.}/$(dirname "${CITADEL_CONFIG:-citadel.yml}")/.citadel"
run_dir=""
if [[ -d "$citadel_dir/runs" ]]; then
  if [[ -z "${CITADEL_ENV:-}" ]]; then
    run_dir="$(find "$citadel_dir/runs" -mindepth 1 -maxdepth 1 -type d -name '*T*Z-*' | sort | tail -n 1)"
  else
    # Newest run of this environment: a newer run of another environment
    # (e.g. a laptop deploy in the same checkout) must not be reported.
    while IFS= read -r dir; do
      if [[ "$(jq -r '.env // ""' "$dir/run.json" 2>/dev/null)" == "$CITADEL_ENV" ]]; then
        run_dir="$dir"
        break
      fi
    done < <(find "$citadel_dir/runs" -mindepth 1 -maxdepth 1 -type d -name '*T*Z-*' | sort -r)
  fi
fi
run_id=""
if [[ -n "$run_dir" ]]; then
  run_id="$(basename "$run_dir")"
fi
image=""
state="$citadel_dir/state/${CITADEL_ENV:-}.json"
if [[ -n "${CITADEL_ENV:-}" && -f "$state" ]]; then
  image="$(jq -r '.image_uri // ""' "$state")"
fi
{
  echo "run-dir=$run_dir"
  echo "run-id=$run_id"
  echo "image-uri=$image"
} >> "$GITHUB_OUTPUT"
