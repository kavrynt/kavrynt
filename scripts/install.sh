#!/usr/bin/env sh
set -eu

REPO="${KAVRYNT_REPO:-kavrynt/kavrynt}"
RELEASE_TAG="${KAVRYNT_VERSION:-v1.0.0-beta.5}"
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
    MINGW* | MSYS* | CYGWIN*) printf 'windows' ;;
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

extract_zip() {
  zip_file="$1"
  dest="$2"
  if command -v unzip >/dev/null 2>&1; then
    unzip -q -o "$zip_file" -d "$dest"
  elif command -v powershell.exe >/dev/null 2>&1; then
    # Git Bash ships no unzip; Windows PowerShell can expand the archive.
    if command -v cygpath >/dev/null 2>&1; then
      zip_file="$(cygpath -w "$zip_file")"
      dest="$(cygpath -w "$dest")"
    fi
    ZIP_FILE="$zip_file" ZIP_DEST="$dest" powershell.exe -NoProfile -NonInteractive -Command \
      'Expand-Archive -LiteralPath $env:ZIP_FILE -DestinationPath $env:ZIP_DEST -Force' ||
      fail "could not extract ${zip_file}"
  else
    fail "unzip or powershell.exe is required to extract the Windows archive"
  fi
}

need_cmd uname
need_cmd awk
need_cmd grep

os="$(detect_os)"
arch="$(detect_arch)"
version="${RELEASE_TAG#v}"
archive_dir="kavryctl_${version}_${os}_${arch}"
if [ "$os" = "windows" ]; then
  archive="${archive_dir}.zip"
  binary="kavryctl.exe"
else
  need_cmd tar
  archive="${archive_dir}.tar.gz"
  binary="kavryctl"
fi
base_url="https://github.com/${REPO}/releases/download/${RELEASE_TAG}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

archive_path="${tmp_dir}/${archive}"
checksums_path="${tmp_dir}/SHA256SUMS"

log "Downloading ${archive} from ${REPO}..."
download "${base_url}/${archive}" "$archive_path"
download "${base_url}/SHA256SUMS" "$checksums_path"

expected="$(awk -v file="$archive" '$2 == file {print $1}' "$checksums_path")"
[ -n "$expected" ] || fail "checksum for ${archive} not found"

actual="$(sha256_file "$archive_path")"
[ "$actual" = "$expected" ] || fail "checksum mismatch for ${archive}"

mkdir -p "$INSTALL_DIR"
if [ "$os" = "windows" ]; then
  extract_zip "$archive_path" "$tmp_dir"
else
  tar -xzf "$archive_path" -C "$tmp_dir" "${archive_dir}/${binary}"
fi
install -m 0755 "${tmp_dir}/${archive_dir}/${binary}" "${INSTALL_DIR}/${binary}"

log "Installed kavryctl to ${INSTALL_DIR}/${binary}"
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *) log "Add ${INSTALL_DIR} to PATH to run kavryctl from any shell." ;;
esac
log "Run: kavryctl version"
