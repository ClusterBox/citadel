#!/usr/bin/env bats

setup() {
  # shellcheck source=../scripts/lib.sh
  source "$BATS_TEST_DIRNAME/../scripts/lib.sh"
}

@test "resolve_env maps main to prod and development to dev" {
  run resolve_env "" main "main=prod,development=dev"
  [ "$status" -eq 0 ] && [ "$output" = "prod" ]
  run resolve_env "" development "main=prod,development=dev"
  [ "$status" -eq 0 ] && [ "$output" = "dev" ]
}

@test "resolve_env tolerates spaces in the map" {
  run resolve_env "" main " main = prod , development=dev "
  [ "$status" -eq 0 ] && [ "$output" = "prod" ]
}

@test "resolve_env: explicit environment wins over the branch" {
  run resolve_env staging main "main=prod"
  [ "$status" -eq 0 ] && [ "$output" = "staging" ]
}

@test "resolve_env fails on an unmapped branch, naming branch and map" {
  run resolve_env "" feature/x "main=prod,development=dev"
  [ "$status" -ne 0 ]
  [[ "$output" == *"feature/x"* ]] && [[ "$output" == *"main=prod,development=dev"* ]]
}

@test "resolve_env fails when the mapped value is empty" {
  run resolve_env "" main "main="
  [ "$status" -ne 0 ]
}

@test "asset_name covers linux/darwin x amd64/arm64" {
  run asset_name 0.1.0 Linux X64;   [ "$output" = "citadel_0.1.0_linux_amd64.tar.gz" ]
  run asset_name 0.1.0 Linux ARM64; [ "$output" = "citadel_0.1.0_linux_arm64.tar.gz" ]
  run asset_name 0.1.0 macOS X64;   [ "$output" = "citadel_0.1.0_darwin_amd64.tar.gz" ]
  run asset_name 0.1.0 macOS ARM64; [ "$output" = "citadel_0.1.0_darwin_arm64.tar.gz" ]
}

@test "asset_name rejects Windows and 32-bit runners" {
  run asset_name 0.1.0 Windows X64
  [ "$status" -ne 0 ] && [[ "$output" == *"Windows"* ]]
  run asset_name 0.1.0 Linux X86
  [ "$status" -ne 0 ] && [[ "$output" == *"X86"* ]]
}

@test "resolve_version: explicit input wins, with or without a leading v" {
  run resolve_version 0.2.0 v0.1.0 /x/_actions/ClusterBox/citadel/v0.1.0/action
  [ "$output" = "release 0.2.0" ]
  run resolve_version v0.2.0 "" ""
  [ "$output" = "release 0.2.0" ]
}

@test "resolve_version rejects a non-semver explicit input" {
  run resolve_version main "" ""
  [ "$status" -ne 0 ] && [[ "$output" == *"main"* ]]
}

@test "resolve_version uses a vX.Y.Z action_ref" {
  run resolve_version "" v0.1.0 ""
  [ "$output" = "release 0.1.0" ]
}

@test "resolve_version falls back to the ref segment of the action path" {
  run resolve_version "" "" /home/runner/work/_actions/ClusterBox/citadel/v0.3.1/action
  [ "$output" = "release 0.3.1" ]
}

@test "resolve_version builds from source for branches, moving tags and local use" {
  run resolve_version "" main /home/runner/work/_actions/ClusterBox/citadel/main/action
  [ "$output" = "source" ]
  run resolve_version "" v0 /home/runner/work/_actions/ClusterBox/citadel/v0/action
  [ "$output" = "source" ]
  run resolve_version "" "" /home/runner/work/app/app/./action
  [ "$output" = "source" ]
}

@test "verify_checksum accepts a matching file and rejects tampering or absence" {
  dir="$BATS_TEST_TMPDIR/rel"; mkdir -p "$dir"
  printf 'binary' > "$dir/citadel_0.1.0_linux_amd64.tar.gz"
  printf '%s  %s\n' "$(sha256_of "$dir/citadel_0.1.0_linux_amd64.tar.gz")" citadel_0.1.0_linux_amd64.tar.gz > "$dir/checksums.txt"
  run verify_checksum "$dir" citadel_0.1.0_linux_amd64.tar.gz
  [ "$status" -eq 0 ]

  printf 'tampered' > "$dir/citadel_0.1.0_linux_amd64.tar.gz"
  run verify_checksum "$dir" citadel_0.1.0_linux_amd64.tar.gz
  [ "$status" -ne 0 ] && [[ "$output" == *"mismatch"* ]]

  run verify_checksum "$dir" citadel_0.1.0_darwin_arm64.tar.gz
  [ "$status" -ne 0 ] && [[ "$output" == *"not listed"* ]]
}

@test "env_file_values parses like citadel's env loader" {
  f="$BATS_TEST_TMPDIR/.env"
  cat > "$f" <<'EOF'
# comment

PLAIN=abc
SPACED =  padded value
DQ="double quoted"
SQ='single quoted'
EQ=a=b=c
EMPTY=
QUOTES_ONLY=""
DOLLAR=$HOME`whoami`%s
EOF
  run env_file_values "$f"
  [ "$status" -eq 0 ]
  expected=$'abc\npadded value\ndouble quoted\nsingle quoted\na=b=c\n$HOME`whoami`%s'
  [ "$output" = "$expected" ]
}

@test "mask_env_file emits one add-mask command per value" {
  f="$BATS_TEST_TMPDIR/.env"
  printf 'A=one\nB=\nC="two"\n' > "$f"
  run mask_env_file "$f"
  [ "$output" = $'::add-mask::one\n::add-mask::two' ]
}

@test "render_summary renders a failed run with its error" {
  f="$BATS_TEST_TMPDIR/run.json"
  cat > "$f" <<'EOF'
{"id":"20260925T100000Z-a1b2","env":"dev","git_sha":"abc1234","status":"failed",
 "steps":[{"name":"ssm-sync","status":"success","duration_ms":1200},
          {"name":"build","status":"failed","duration_ms":45000,"error":"docker: no space | left"},
          {"name":"cdk","status":"skipped","duration_ms":0}]}
EOF
  run render_summary "$f" "111.dkr.ecr.us-east-1.amazonaws.com/demo-dev-repo:abc1234"
  [ "$status" -eq 0 ]
  expected="### citadel deploy: dev — failed

- **Run:** \`20260925T100000Z-a1b2\`
- **Commit:** \`abc1234\`
- **Image:** \`111.dkr.ecr.us-east-1.amazonaws.com/demo-dev-repo:abc1234\`

| Step | Status | Duration | Error |
|---|---|---|---|
| ssm-sync | success | 1.2s |  |
| build | failed | 45s | docker: no space \\| left |
| cdk | skipped | – |  |"
  [ "$output" = "$expected" ]
}

@test "render_summary omits the image line when unknown" {
  f="$BATS_TEST_TMPDIR/run.json"
  echo '{"id":"r","env":"dev","git_sha":"s","status":"success","steps":[]}' > "$f"
  run render_summary "$f" ""
  [[ "$output" != *"Image"* ]]
}
