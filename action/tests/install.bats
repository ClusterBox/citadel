#!/usr/bin/env bats

setup() {
  SCRIPTS="$BATS_TEST_DIRNAME/../scripts"
  source "$SCRIPTS/lib.sh"
  export RUNNER_TEMP="$BATS_TEST_TMPDIR/runner" RUNNER_TOOL_CACHE="$BATS_TEST_TMPDIR/toolcache"
  mkdir -p "$RUNNER_TEMP" "$RUNNER_TOOL_CACHE"
  export GITHUB_PATH="$BATS_TEST_TMPDIR/path"; : > "$GITHUB_PATH"
  export RUNNER_OS=Linux RUNNER_ARCH=X64
  REL="$BATS_TEST_TMPDIR/release/v9.9.9"; mkdir -p "$REL" "$BATS_TEST_TMPDIR/pkg"
  printf '#!/bin/sh\necho "citadel version 9.9.9"\n' > "$BATS_TEST_TMPDIR/pkg/citadel"
  chmod +x "$BATS_TEST_TMPDIR/pkg/citadel"
  tar -czf "$REL/citadel_9.9.9_linux_amd64.tar.gz" -C "$BATS_TEST_TMPDIR/pkg" citadel
  printf '%s  %s\n' "$(sha256_of "$REL/citadel_9.9.9_linux_amd64.tar.gz")" citadel_9.9.9_linux_amd64.tar.gz > "$REL/checksums.txt"
  # Fake curl: copies "<base>/<file>" from the local release dir to -o <dest>.
  mkdir -p "$BATS_TEST_TMPDIR/bin"
  cat > "$BATS_TEST_TMPDIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
out=""; url=""
while [[ $# -gt 0 ]]; do
  case "$1" in -o) out="$2"; shift 2 ;; -*) shift ;; *) url="$1"; shift ;; esac
done
cp "${url#file://}" "$out"
EOF
  chmod +x "$BATS_TEST_TMPDIR/bin/curl"
  export PATH="$BATS_TEST_TMPDIR/bin:$PATH"
  export CITADEL_RELEASE_BASE_URL="file://$BATS_TEST_TMPDIR/release"
  export CITADEL_INSTALL_MODE=release CITADEL_INSTALL_VERSION=9.9.9
}

@test "installs a verified release into the tool cache and puts it on PATH" {
  run bash "$SCRIPTS/install.sh"
  [ "$status" -eq 0 ]
  [[ "$output" == *"citadel version 9.9.9"* ]]
  [ "$(cat "$GITHUB_PATH")" = "$RUNNER_TOOL_CACHE/citadel/9.9.9/linux_amd64" ]
  [ -x "$RUNNER_TOOL_CACHE/citadel/9.9.9/linux_amd64/citadel" ]
}

@test "a cached install is reused without downloading" {
  bash "$SCRIPTS/install.sh"
  rm "$BATS_TEST_TMPDIR/bin/curl"; printf '#!/bin/sh\nexit 99\n' > "$BATS_TEST_TMPDIR/bin/curl"; chmod +x "$BATS_TEST_TMPDIR/bin/curl"
  run bash "$SCRIPTS/install.sh"
  [ "$status" -eq 0 ]
}

@test "a checksum mismatch fails and installs nothing" {
  echo "0000000000000000000000000000000000000000000000000000000000000000  citadel_9.9.9_linux_amd64.tar.gz" > "$REL/checksums.txt"
  run bash "$SCRIPTS/install.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *"mismatch"* ]]
  [ ! -e "$RUNNER_TOOL_CACHE/citadel/9.9.9/linux_amd64/citadel" ]
}

@test "source mode announces an unreleased build" {
  export CITADEL_INSTALL_MODE=source GITHUB_ACTION_PATH="$BATS_TEST_TMPDIR/checkout/action"
  mkdir -p "$GITHUB_ACTION_PATH"
  # Fake go: "builds" by writing a script to the -o path.
  cat > "$BATS_TEST_TMPDIR/bin/go" <<'EOF'
#!/usr/bin/env bash
while [[ $# -gt 0 ]]; do [[ "$1" == -o ]] && { printf '#!/bin/sh\necho "citadel version source"\n' > "$2"; chmod +x "$2"; }; shift; done
EOF
  chmod +x "$BATS_TEST_TMPDIR/bin/go"
  run bash "$SCRIPTS/install.sh"
  [ "$status" -eq 0 ]
  [[ "$output" == *"::notice"*"unreleased citadel"* ]]
  [[ "$output" == *"citadel version source"* ]]
}

fake_go_recording_args() {
  cat > "$BATS_TEST_TMPDIR/bin/go" <<'EOF2'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$BATS_TEST_TMPDIR/go_args"
while [[ $# -gt 0 ]]; do [[ "$1" == -o ]] && { printf '#!/bin/sh\necho "citadel version source"\n' > "$2"; chmod +x "$2"; }; shift; done
EOF2
  chmod +x "$BATS_TEST_TMPDIR/bin/go"
}

@test "source mode outside a git checkout labels the build with the action ref" {
  export CITADEL_INSTALL_MODE=source CITADEL_ACTION_REF=main GITHUB_ACTION_PATH="$BATS_TEST_TMPDIR/checkout/action"
  mkdir -p "$GITHUB_ACTION_PATH"
  fake_go_recording_args
  run bash "$SCRIPTS/install.sh"
  [ "$status" -eq 0 ]
  [[ "$output" == *"built from source (main)"* ]]
  grep -q -- '-X main.version=source-main' "$BATS_TEST_TMPDIR/go_args"
}

@test "source mode without git or an action ref labels the build with the ref segment of the action path" {
  export CITADEL_INSTALL_MODE=source CITADEL_ACTION_REF="" GITHUB_ACTION_PATH="$BATS_TEST_TMPDIR/_actions/ClusterBox/citadel/feat-x/action"
  mkdir -p "$GITHUB_ACTION_PATH"
  fake_go_recording_args
  run bash "$SCRIPTS/install.sh"
  [ "$status" -eq 0 ]
  [[ "$output" == *"built from source (feat-x)"* ]]
  grep -q -- '-X main.version=source-feat-x' "$BATS_TEST_TMPDIR/go_args"
}

@test "a failed download explains that the release may not exist or still be publishing" {
  printf '#!/bin/sh\nexit 22\n' > "$BATS_TEST_TMPDIR/bin/curl"
  run bash "$SCRIPTS/install.sh"
  [ "$status" -eq 1 ]
  [[ "$output" == *"citadel: could not download $CITADEL_RELEASE_BASE_URL/v9.9.9/citadel_9.9.9_linux_amd64.tar.gz — does release v9.9.9 exist (tags without a GitHub Release, e.g. v0.1.0/v0.2.0, cannot be installed) or is it still publishing?"* ]]
  [ ! -e "$RUNNER_TOOL_CACHE/citadel/9.9.9/linux_amd64/citadel" ]
}

@test "a failed checksums download gets the same explanation" {
  rm "$REL/checksums.txt"
  printf '#!/usr/bin/env bash\nout=""; url=""\nwhile [[ $# -gt 0 ]]; do case "$1" in -o) out="$2"; shift 2 ;; -*) shift ;; *) url="$1"; shift ;; esac; done\ncp "${url#file://}" "$out" 2>/dev/null || exit 22\n' > "$BATS_TEST_TMPDIR/bin/curl"
  run bash "$SCRIPTS/install.sh"
  [ "$status" -eq 1 ]
  [[ "$output" == *"citadel: could not download $CITADEL_RELEASE_BASE_URL/v9.9.9/checksums.txt — does release v9.9.9 exist"* ]]
}
