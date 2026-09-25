#!/usr/bin/env bash
# Helpers for the citadel GitHub Action. Sourced by the step scripts and by
# action/tests/*.bats; defines functions only, with no side effects.

# trim S — strips leading and trailing whitespace.
trim() {
  local s="$1"
  s="${s#"${s%%[![:space:]]*}"}"
  s="${s%"${s##*[![:space:]]}"}"
  printf '%s' "$s"
}

# resolve_env EXPLICIT BRANCH BRANCH_MAP — prints the citadel environment:
# EXPLICIT when set, else BRANCH's entry in BRANCH_MAP ("main=prod,dev=dev").
resolve_env() {
  local explicit="$1" branch="$2" map="$3" pair key value
  if [[ -n "$explicit" ]]; then
    printf '%s\n' "$explicit"
    return 0
  fi
  local -a pairs
  IFS=',' read -r -a pairs <<< "$map"
  for pair in "${pairs[@]}"; do
    [[ "$pair" == *=* ]] || continue
    key="$(trim "${pair%%=*}")"
    value="$(trim "${pair#*=}")"
    if [[ "$key" == "$branch" && -n "$value" ]]; then
      printf '%s\n' "$value"
      return 0
    fi
  done
  echo "citadel: branch '$branch' is not in branch-map '$map'; set the 'environment' input or add '$branch=<env>' to 'branch-map'" >&2
  return 1
}

# asset_os_arch RUNNER_OS RUNNER_ARCH — prints "<os>_<arch>" as used in
# release asset names (linux|darwin, amd64|arm64).
asset_os_arch() {
  local os arch
  case "$1" in
    Linux) os=linux ;;
    macOS) os=darwin ;;
    *) echo "citadel: unsupported runner OS '$1' (supported: Linux, macOS)" >&2; return 1 ;;
  esac
  case "$2" in
    X64) arch=amd64 ;;
    ARM64) arch=arm64 ;;
    *) echo "citadel: unsupported runner architecture '$2' (supported: X64, ARM64)" >&2; return 1 ;;
  esac
  printf '%s_%s\n' "$os" "$arch"
}

# asset_name VERSION RUNNER_OS RUNNER_ARCH — the release archive for this
# runner. Must match name_template in .goreleaser.yaml.
asset_name() {
  local osarch
  osarch="$(asset_os_arch "$2" "$3")" || return 1
  printf 'citadel_%s_%s.tar.gz\n' "$1" "$osarch"
}

# resolve_version INPUT ACTION_REF ACTION_PATH — prints "release X.Y.Z" or
# "source". Precedence: the explicit input; a vX.Y.Z action_ref; a vX.Y.Z ref
# segment in the action's download path (…/_actions/<owner>/<repo>/<ref>/action,
# needed because action_ref can be empty inside composite actions); otherwise
# build from the action's own checkout (branches, SHAs, moving tags, ./action).
resolve_version() {
  local input="$1" ref="$2" path="$3" seg semver='^v?([0-9]+\.[0-9]+\.[0-9]+)$'
  if [[ -n "$input" ]]; then
    if [[ "$input" =~ $semver ]]; then
      printf 'release %s\n' "${BASH_REMATCH[1]}"
      return 0
    fi
    echo "citadel: version input '$input' is not X.Y.Z" >&2
    return 1
  fi
  if [[ "$ref" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
    printf 'release %s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ -n "$path" ]]; then
    seg="$(basename "$(dirname "$path")")"
    if [[ "$seg" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
      printf 'release %s\n' "${BASH_REMATCH[1]}"
      return 0
    fi
  fi
  printf 'source\n'
}

# sha256_of FILE — prints FILE's SHA-256 (GNU sha256sum or macOS shasum).
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# verify_checksum DIR ASSET — checks DIR/ASSET against its entry in
# DIR/checksums.txt ("<sha256>  <name>").
verify_checksum() {
  local dir="$1" asset="$2" want got
  want="$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "$dir/checksums.txt")"
  if [[ -z "$want" ]]; then
    echo "citadel: $asset is not listed in checksums.txt" >&2
    return 1
  fi
  got="$(sha256_of "$dir/$asset")"
  if [[ "$got" != "$want" ]]; then
    echo "citadel: checksum mismatch for $asset (want $want, got $got)" >&2
    return 1
  fi
}

# strip_quote_chars S — removes all leading and trailing " and ' characters,
# like Go's strings.Trim(s, `"'`) in citadel's env loader.
strip_quote_chars() {
  local s="$1"
  while [[ "$s" == \"* || "$s" == \'* ]]; do s="${s:1}"; done
  while [[ "$s" == *\" || "$s" == *\' ]]; do s="${s:0:${#s}-1}"; done
  printf '%s' "$s"
}

# env_file_values FILE — prints each non-empty value in FILE, one per line,
# parsed exactly as internal/env.Load does: trim the line, skip blanks and
# "#" comments, split on the first "=", trim the value, strip quote chars.
# A trimmed, non-empty, non-comment line with no "=" is most likely the
# continuation of a pasted multi-line secret (e.g. a PEM key); it is printed
# as-is so mask_env_file still masks it.
env_file_values() {
  local line value
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="$(trim "$line")"
    [[ -z "$line" || "$line" == \#* ]] && continue
    if [[ "$line" != *=* ]]; then
      printf '%s\n' "$line"
      continue
    fi
    value="$(strip_quote_chars "$(trim "${line#*=}")")"
    if [[ -n "$value" ]]; then
      printf '%s\n' "$value"
    fi
  done < "$1"
}

# mask_env_file FILE — emits a workflow command masking every value in FILE.
# Each value is escaped like @actions/core's escapeData before being placed
# in the ::add-mask:: command, in order: '%' -> '%25', then CR -> '%0D', then
# LF -> '%0A'. Without this a value containing a literal "%25" would be
# masked as its decoded form, and a masked value spanning "lines" (CR/LF)
# would break the single-line workflow command.
mask_env_file() {
  local v escaped
  while IFS= read -r v; do
    escaped="${v//%/%25}"
    escaped="${escaped//$'\r'/%0D}"
    escaped="${escaped//$'\n'/%0A}"
    printf '::add-mask::%s\n' "$escaped"
  done < <(env_file_values "$1")
}

# render_summary RUN_JSON IMAGE_URI — markdown job summary for a citadel run.
render_summary() {
  jq -r --arg image "$2" '
    def dur: if .status == "skipped" then "–"
             else "\(((.duration_ms // 0) / 100 | round) / 10)s" end;
    def cell: tostring | gsub("\\|"; "\\|") | gsub("`"; "\\`") | gsub("\n"; " ");
    "### citadel deploy: \(.env) — \(.status)",
    "",
    "- **Run:** `\(.id)`",
    "- **Commit:** `\(.git_sha)`",
    (if $image != "" then "- **Image:** `\($image)`" else empty end),
    "",
    "| Step | Status | Duration | Error |",
    "|---|---|---|---|",
    (.steps[] | "| \(.name) | \(.status) | \(dur) | \(.error // "" | cell) |")
  ' "$1"
}
