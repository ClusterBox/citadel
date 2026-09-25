#!/usr/bin/env bash
# Appends the run's steps to the job summary (no-op when citadel never started a run).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

run_dir="${CITADEL_RUN_DIR:-}"
if [[ -z "$run_dir" || ! -f "$run_dir/run.json" ]]; then
  echo "citadel: no run recorded; skipping job summary"
  exit 0
fi
render_summary "$run_dir/run.json" "${CITADEL_IMAGE_URI:-}" >> "${GITHUB_STEP_SUMMARY:?}"
