#!/bin/sh

set -eu

repository="${REALY_GITHUB_REPOSITORY:-KDF5000/realy}"
release_version="${REALY_VERSION:-latest}"
install_dir="${REALY_INSTALL_DIR:-${HOME}/.local/bin}"
config_file="${REALY_CONFIG_FILE:-${XDG_CONFIG_HOME:-${HOME}/.config}/realy/node.json}"
data_dir="${REALY_DATA_DIR:-${XDG_CACHE_HOME:-${HOME}/.cache}/realy}"
server_url="${REALY_SERVER_URL:-http://127.0.0.1:8787}"
node_id="${REALY_NODE_ID:-$(hostname 2>/dev/null || printf 'realy-node')}"
capacity="${REALY_NODE_CAPACITY:-2}"
node_token="${REALY_NODE_TOKEN:-}"
runtime_choice="${REALY_RUNTIME:-auto}"
force_config=0

usage() {
  cat <<'EOF'
Install Realy Node and generate its configuration.

Usage:
  install.sh [options]

Options:
  --server URL       Realy Server URL (default: http://127.0.0.1:8787)
  --node-id ID       Node identity (default: local hostname)
  --capacity N       Maximum concurrent runs (default: 2)
  --runtime VALUE    auto, codex, trae, or both (default: auto)
  --token TOKEN      Node authentication token
  --version VERSION  Release tag such as v0.1.0 (default: latest)
  --install-dir DIR  Binary directory (default: ~/.local/bin)
  --config FILE      Configuration path (default: ~/.config/realy/node.json)
  --force            Replace an existing config after creating a backup
  -h, --help         Show this help

Environment variables with the REALY_ prefix provide the same defaults.
EOF
}

fail() {
  printf 'realy installer: %s\n' "$*" >&2
  exit 1
}

need_value() {
  [ "$#" -ge 2 ] || fail "$1 requires a value"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --server) need_value "$@"; server_url=$2; shift 2 ;;
    --node-id) need_value "$@"; node_id=$2; shift 2 ;;
    --capacity) need_value "$@"; capacity=$2; shift 2 ;;
    --runtime) need_value "$@"; runtime_choice=$2; shift 2 ;;
    --token) need_value "$@"; node_token=$2; shift 2 ;;
    --version) need_value "$@"; release_version=$2; shift 2 ;;
    --install-dir) need_value "$@"; install_dir=$2; shift 2 ;;
    --config) need_value "$@"; config_file=$2; shift 2 ;;
    --force) force_config=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option: $1" ;;
  esac
done

case "$capacity" in
  ''|*[!0-9]*) fail "--capacity must be a positive integer" ;;
esac
[ "$capacity" -gt 0 ] || fail "--capacity must be a positive integer"

case "$runtime_choice" in
  auto|codex|trae|both) ;;
  *) fail "--runtime must be auto, codex, trae, or both" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s)" in
  Darwin) target_os=darwin ;;
  Linux) target_os=linux ;;
  *) fail "only macOS and Linux are currently supported" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) target_arch=amd64 ;;
  arm64|aarch64) target_arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

asset="realy_${target_os}_${target_arch}.tar.gz"
if [ "$release_version" = latest ]; then
  release_base="https://github.com/${repository}/releases/latest/download"
else
  release_base="https://github.com/${repository}/releases/download/${release_version}"
fi
release_base="${REALY_DOWNLOAD_BASE_URL:-$release_base}"

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/realy-install.XXXXXX")
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM

printf 'Downloading Realy for %s/%s...\n' "$target_os" "$target_arch"
curl -fsSL --retry 3 --connect-timeout 15 -o "$temporary_dir/$asset" "$release_base/$asset"
curl -fsSL --retry 3 --connect-timeout 15 -o "$temporary_dir/checksums.txt" "$release_base/checksums.txt"

expected_checksum=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1; exit }' "$temporary_dir/checksums.txt")
[ -n "$expected_checksum" ] || fail "checksum for $asset was not found"
if command -v sha256sum >/dev/null 2>&1; then
  actual_checksum=$(sha256sum "$temporary_dir/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual_checksum=$(shasum -a 256 "$temporary_dir/$asset" | awk '{print $1}')
else
  fail "sha256sum or shasum is required to verify the download"
fi
[ "$expected_checksum" = "$actual_checksum" ] || fail "download checksum verification failed"

mkdir -p "$temporary_dir/package"
tar -xzf "$temporary_dir/$asset" -C "$temporary_dir/package"
[ -f "$temporary_dir/package/realy-node" ] || fail "release does not contain realy-node"
[ -f "$temporary_dir/package/realy-tool" ] || fail "release does not contain realy-tool"

mkdir -p "$install_dir"
install -m 0755 "$temporary_dir/package/realy-node" "$install_dir/realy-node"
install -m 0755 "$temporary_dir/package/realy-tool" "$install_dir/realy-tool"
if [ -f "$temporary_dir/package/realyctl" ]; then
  install -m 0755 "$temporary_dir/package/realyctl" "$install_dir/realyctl"
fi

find_command() {
  command -v "$1" 2>/dev/null || true
}

codex_command=$(find_command codex)
trae_command=$(find_command traex)
[ -n "$trae_command" ] || trae_command=$(find_command trae-cli)

include_codex=0
include_trae=0
case "$runtime_choice" in
  auto)
    [ -n "$codex_command" ] && include_codex=1
    [ -n "$trae_command" ] && include_trae=1
    ;;
  codex) include_codex=1 ;;
  trae) include_trae=1 ;;
  both) include_codex=1; include_trae=1 ;;
esac

[ "$include_codex" -eq 0 ] || [ -n "$codex_command" ] || fail "Codex CLI was not found in PATH"
[ "$include_trae" -eq 0 ] || [ -n "$trae_command" ] || fail "Trae CLI (traex or trae-cli) was not found in PATH"
[ "$include_codex" -eq 1 ] || [ "$include_trae" -eq 1 ] || fail "no supported Runtime found; install Codex or Trae, or pass --runtime"

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

if [ -f "$config_file" ] && [ "$force_config" -ne 1 ]; then
  printf 'Keeping existing config: %s\n' "$config_file"
else
  config_dir=$(dirname "$config_file")
  mkdir -p "$config_dir"
  if [ -f "$config_file" ]; then
    backup_file="${config_file}.backup.$(date +%Y%m%d%H%M%S)"
    cp "$config_file" "$backup_file"
    printf 'Backed up existing config to %s\n' "$backup_file"
  fi

  escaped_server=$(json_escape "$server_url")
  escaped_node=$(json_escape "$node_id")
  escaped_install_dir=$(json_escape "$install_dir")
  escaped_data_dir=$(json_escape "$data_dir")
  escaped_token=$(json_escape "$node_token")
  runtime_json=""
  separator=""
  if [ "$include_codex" -eq 1 ]; then
    escaped_command=$(json_escape "$codex_command")
    runtime_json="${runtime_json}${separator}
    {\"id\": \"${escaped_node}/codex\", \"kind\": \"codex\", \"provider\": \"codex\", \"protocol\": \"app-server\", \"command\": \"${escaped_command}\", \"tool_dir\": \"${escaped_install_dir}\", \"sandbox\": \"workspace-write\", \"work_root\": \"${escaped_data_dir}/runs\", \"ephemeral\": true}"
    separator=","
  fi
  if [ "$include_trae" -eq 1 ]; then
    escaped_command=$(json_escape "$trae_command")
    runtime_json="${runtime_json}${separator}
    {\"id\": \"${escaped_node}/trae\", \"kind\": \"trae\", \"provider\": \"trae\", \"protocol\": \"app-server\", \"command\": \"${escaped_command}\", \"tool_dir\": \"${escaped_install_dir}\", \"sandbox\": \"workspace-write\", \"work_root\": \"${escaped_data_dir}/runs\", \"ephemeral\": true}"
  fi

  token_line=""
  [ -z "$node_token" ] || token_line="  \"token\": \"${escaped_token}\","
  umask 077
  cat >"$config_file" <<EOF
{
  "server": "${escaped_server}",
${token_line}
  "workspace_root": "${escaped_data_dir}/workspaces",
  "node": {
    "id": "${escaped_node}",
    "capacity": ${capacity},
    "runtimes": []
  },
  "runtimes": [${runtime_json}
  ],
  "bindings": []
}
EOF
  printf 'Created config: %s\n' "$config_file"
fi

printf '\nRealy Node installed successfully.\n'
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) printf 'Add %s to PATH before using the installed commands.\n' "$install_dir" ;;
esac
printf 'Start the node with:\n  %s/realy-node -config %s\n' "$install_dir" "$config_file"
