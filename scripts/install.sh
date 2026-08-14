#!/usr/bin/env sh
set -eu

REPO="${KAVRYNT_REPO:-kavrynt/kavrynt}"
VERSION="${KAVRYNT_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/.kavrynt/bin}"

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

detect_os() {
  case "$(uname -s)" in
    Linux) printf 'linux' ;;
    Darwin) printf 'darwin' ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) printf 'amd64' ;;
    arm64 | aarch64) printf 'arm64' ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac
}

download() {
  url="$1"
  output="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$output"
  else
    fail "curl or wget is required"
  fi
}

sha256_file() {
  file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    fail "sha256sum or shasum is required for checksum verification"
  fi
}

need_cmd uname
need_cmd tar
need_cmd awk
need_cmd grep

os="$(detect_os)"
arch="$(detect_arch)"
archive="kavryctl_${os}_${arch}.tar.gz"

if [ "$VERSION" = "latest" ]; then
  base_url="https://github.com/${REPO}/releases/latest/download"
else
  base_url="https://github.com/${REPO}/releases/download/${VERSION}"
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

archive_path="${tmp_dir}/${archive}"
checksums_path="${tmp_dir}/checksums.txt"

log "Downloading ${archive} from ${REPO}..."
download "${base_url}/${archive}" "$archive_path"
download "${base_url}/checksums.txt" "$checksums_path"

expected="$(awk -v file="$archive" '$2 == file {print $1}' "$checksums_path")"
[ -n "$expected" ] || fail "checksum for ${archive} not found"

actual="$(sha256_file "$archive_path")"
[ "$actual" = "$expected" ] || fail "checksum mismatch for ${archive}"

mkdir -p "$INSTALL_DIR"
tar -xzf "$archive_path" -C "$tmp_dir" kavryctl
install -m 0755 "${tmp_dir}/kavryctl" "${INSTALL_DIR}/kavryctl"

log "Installed kavryctl to ${INSTALL_DIR}/kavryctl"
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *) log "Add ${INSTALL_DIR} to PATH to run kavryctl from any shell." ;;
esac
log "Run: kavryctl version"
