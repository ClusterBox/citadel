#!/usr/bin/env bats

setup() {
  SCRIPTS="$BATS_TEST_DIRNAME/../scripts"
  export RUNNER_TEMP="$BATS_TEST_TMPDIR/runner"; mkdir -p "$RUNNER_TEMP"
  export GITHUB_OUTPUT="$BATS_TEST_TMPDIR/output"; : > "$GITHUB_OUTPUT"
  export FAKE_DIR="$BATS_TEST_TMPDIR/fake"; mkdir -p "$FAKE_DIR/bin"
  cat > "$FAKE_DIR/bin/citadel" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$FAKE_DIR/args"
if [[ -n "${CITADEL_ENV_FILE_CONTENT+x}" ]]; then echo set > "$FAKE_DIR/content_var"; else echo unset > "$FAKE_DIR/content_var"; fi
prev=""
for a in "$@"; do
  if [[ "$prev" == "--env-file" ]]; then
    echo "$a" > "$FAKE_DIR/env_path"
    stat -c %a "$a" > "$FAKE_DIR/env_mode" 2>/dev/null || stat -f %Lp "$a" > "$FAKE_DIR/env_mode"
    cp "$a" "$FAKE_DIR/env_copy"
  fi
  prev="$a"
done
echo "fake-citadel-called"
exit "${FAKE_EXIT:-0}"
EOF
  chmod +x "$FAKE_DIR/bin/citadel"
  export PATH="$FAKE_DIR/bin:$PATH"
  export CITADEL_ENV=dev CITADEL_MESSAGE="ship it" CITADEL_WORKING_DIRECTORY="$BATS_TEST_TMPDIR" CITADEL_CONFIG=citadel.yml
  : > "$BATS_TEST_TMPDIR/citadel.yml"
}

@test "writes the env file privately, masks values first, passes args, then removes it" {
  export CITADEL_ENV_FILE_CONTENT=$'DB=secret-one\nTOKEN="secret two"'
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  mask_line=$(printf '%s\n' "$output" | grep -n '::add-mask::secret-one' | cut -d: -f1)
  call_line=$(printf '%s\n' "$output" | grep -n 'fake-citadel-called' | cut -d: -f1)
  [ -n "$mask_line" ]
  [ "$mask_line" -lt "$call_line" ]
  [[ "$output" == *"::add-mask::secret two"* ]]
  [ "$(cat "$FAKE_DIR/env_mode")" = "600" ]
  [ ! -e "$(cat "$FAKE_DIR/env_path")" ]
  expected=$'deploy\n--config\ncitadel.yml\n--env\ndev\n-m\nship it\n--env-file\n'"$(cat "$FAKE_DIR/env_path")"$'\n--wait'
  [ "$(cat "$FAKE_DIR/args")" = "$expected" ]
}

@test "writes tricky values verbatim and never evaluates them" {
  export CITADEL_ENV_FILE_CONTENT='A=$HOME`touch /tmp/pwned-citadel`%s "q" x=y'
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  [ "$(cat "$FAKE_DIR/env_copy")" = "$CITADEL_ENV_FILE_CONTENT" ]
  [ ! -e /tmp/pwned-citadel ]
}

@test "skip-secrets passes --skip-ssm and writes no env file" {
  export CITADEL_SKIP_SECRETS=true CITADEL_ENV_FILE_CONTENT=""
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  [[ "$output" != *"::add-mask::"* ]]
  grep -qx -- '--skip-ssm' "$FAKE_DIR/args"
  run grep -qx -- '--env-file' "$FAKE_DIR/args"
  [ "$status" -ne 0 ]
}

@test "an empty env-file without skip-secrets fails before calling citadel" {
  export CITADEL_ENV_FILE_CONTENT=""
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *"env-file"* && "$output" == *"skip-secrets"* ]]
  [ ! -e "$FAKE_DIR/args" ]
}

@test "a failing deploy propagates its exit code and still removes the env file" {
  export CITADEL_ENV_FILE_CONTENT="A=b" FAKE_EXIT=3
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 3 ]
  [ ! -e "$(cat "$FAKE_DIR/env_path")" ]
}

@test "deploy-infra and wait flags map to citadel flags" {
  export CITADEL_ENV_FILE_CONTENT="A=b" CITADEL_DEPLOY_INFRA=true CITADEL_WAIT=false
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  grep -qx -- '--deploy-infra' "$FAKE_DIR/args"
  run grep -qx -- '--wait' "$FAKE_DIR/args"
  [ "$status" -ne 0 ]
}

@test "the default message is the head commit subject and short sha" {
  repo="$BATS_TEST_TMPDIR/repo"; mkdir -p "$repo"
  git -C "$repo" init -q && git -C "$repo" -c user.email=t@t -c user.name=t commit -q --allow-empty -m "feat: add thing"
  export CITADEL_WORKING_DIRECTORY="$repo" CITADEL_MESSAGE="" CITADEL_ENV_FILE_CONTENT="A=b"
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  sha=$(git -C "$repo" rev-parse --short HEAD)
  grep -qx -- "feat: add thing ($sha)" "$FAKE_DIR/args"
}

@test "citadel does not inherit the env-file content variable" {
  export CITADEL_ENV_FILE_CONTENT="A=b"
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  [ "$(cat "$FAKE_DIR/content_var")" = "unset" ]
}

@test "an existing config file is passed with --config" {
  export CITADEL_ENV_FILE_CONTENT="A=b"
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  grep -qx -- '--config' "$FAKE_DIR/args"
  grep -qx -- 'citadel.yml' "$FAKE_DIR/args"
}

@test "a missing config file omits --config so citadel's citadel.yaml fallback applies" {
  rm "$BATS_TEST_TMPDIR/citadel.yml"
  : > "$BATS_TEST_TMPDIR/citadel.yaml"
  export CITADEL_ENV_FILE_CONTENT="A=b"
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  run grep -qx -- '--config' "$FAKE_DIR/args"
  [ "$status" -ne 0 ]
  expected=$'deploy\n--env\ndev\n-m\nship it\n--env-file\n'"$(cat "$FAKE_DIR/env_path")"$'\n--wait'
  [ "$(cat "$FAKE_DIR/args")" = "$expected" ]
}

@test "a missing non-default config path is still passed so citadel fails loudly" {
  export CITADEL_CONFIG="services/api/citadel.yml"
  export CITADEL_ENV_FILE_CONTENT="A=b"
  run bash "$SCRIPTS/deploy.sh"
  [ "$status" -eq 0 ]
  grep -qx -- '--config' "$FAKE_DIR/args"
  grep -qx -- 'services/api/citadel.yml' "$FAKE_DIR/args"
}
