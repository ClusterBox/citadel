#!/usr/bin/env bats

setup() {
  SCRIPTS="$BATS_TEST_DIRNAME/../scripts"
  export GITHUB_OUTPUT="$BATS_TEST_TMPDIR/output"; : > "$GITHUB_OUTPUT"
  export GITHUB_STEP_SUMMARY="$BATS_TEST_TMPDIR/summary"; : > "$GITHUB_STEP_SUMMARY"
}

@test "resolve-env writes the environment output" {
  export CITADEL_ENVIRONMENT="" CITADEL_BRANCH_MAP="main=prod,development=dev" GITHUB_REF_NAME=development
  run bash "$SCRIPTS/resolve-env.sh"
  [ "$status" -eq 0 ]
  grep -qx 'environment=dev' "$GITHUB_OUTPUT"
}

@test "resolve-env fails on an unmapped branch" {
  export CITADEL_BRANCH_MAP="main=prod" GITHUB_REF_NAME=feature/x
  run bash "$SCRIPTS/resolve-env.sh"
  [ "$status" -ne 0 ]
}

@test "version writes mode and version outputs" {
  export CITADEL_VERSION_INPUT="" CITADEL_ACTION_REF=v1.2.3 GITHUB_ACTION_PATH=""
  run bash "$SCRIPTS/version.sh"
  [ "$status" -eq 0 ]
  grep -qx 'mode=release' "$GITHUB_OUTPUT"
  grep -qx 'version=1.2.3' "$GITHUB_OUTPUT"
}

@test "version reports source mode with an empty version" {
  export CITADEL_VERSION_INPUT="" CITADEL_ACTION_REF=main GITHUB_ACTION_PATH=/x/main/action
  run bash "$SCRIPTS/version.sh"
  [ "$status" -eq 0 ]
  grep -qx 'mode=source' "$GITHUB_OUTPUT"
  grep -qx 'version=' "$GITHUB_OUTPUT"
}

@test "cdk-go-mod reports cdk/go.mod next to the config, and nothing when absent" {
  w="$BATS_TEST_TMPDIR/w"; mkdir -p "$w/svc/cdk"
  export CITADEL_WORKING_DIRECTORY="$w" CITADEL_CONFIG=svc/citadel.yml
  run bash "$SCRIPTS/cdk-go-mod.sh"
  [ ! -s "$GITHUB_OUTPUT" ]
  touch "$w/svc/cdk/go.mod"
  run bash "$SCRIPTS/cdk-go-mod.sh"
  grep -qx "go-mod=$w/svc/./cdk/go.mod" "$GITHUB_OUTPUT" || grep -qx "go-mod=$w/svc/cdk/go.mod" "$GITHUB_OUTPUT"
}

@test "locate-run finds the newest run and the image from state" {
  w="$BATS_TEST_TMPDIR/w"; mkdir -p "$w/.citadel/runs/20260925T100000Z-aaaa" "$w/.citadel/runs/20260925T110000Z-bbbb" "$w/.citadel/state"
  echo '{"image_uri":"repo:abc"}' > "$w/.citadel/state/dev.json"
  export CITADEL_WORKING_DIRECTORY="$w" CITADEL_CONFIG=citadel.yml CITADEL_ENV=dev
  run bash "$SCRIPTS/locate-run.sh"
  [ "$status" -eq 0 ]
  grep -qx "run-id=20260925T110000Z-bbbb" "$GITHUB_OUTPUT"
  grep -qx "run-dir=$w/./.citadel/runs/20260925T110000Z-bbbb" "$GITHUB_OUTPUT" || grep -qx "run-dir=$w/.citadel/runs/20260925T110000Z-bbbb" "$GITHUB_OUTPUT"
  grep -qx "image-uri=repo:abc" "$GITHUB_OUTPUT"
}

@test "locate-run writes empty outputs when citadel never started a run" {
  export CITADEL_WORKING_DIRECTORY="$BATS_TEST_TMPDIR" CITADEL_CONFIG=citadel.yml CITADEL_ENV=dev
  run bash "$SCRIPTS/locate-run.sh"
  [ "$status" -eq 0 ]
  grep -qx "run-dir=" "$GITHUB_OUTPUT"
  grep -qx "run-id=" "$GITHUB_OUTPUT"
}

@test "summary appends the rendered run, and is a no-op without a run" {
  export CITADEL_RUN_DIR=""
  run bash "$SCRIPTS/summary.sh"
  [ "$status" -eq 0 ]
  [ ! -s "$GITHUB_STEP_SUMMARY" ]
  d="$BATS_TEST_TMPDIR/run"; mkdir -p "$d"
  echo '{"id":"r1","env":"dev","git_sha":"s","status":"success","steps":[]}' > "$d/run.json"
  export CITADEL_RUN_DIR="$d" CITADEL_IMAGE_URI=""
  run bash "$SCRIPTS/summary.sh"
  grep -q "citadel deploy: dev — success" "$GITHUB_STEP_SUMMARY"
}
