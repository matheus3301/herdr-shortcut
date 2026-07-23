#!/bin/sh
# Install-time build. Builds from source with a compatible Go toolchain, or
# otherwise downloads and checksum-verifies the release archive for this exact
# manifest version. Fails closed on any unsupported or unverifiable input.
set -eu

REPO="matheus3301/herdr-shortcut"
BIN_NAME="herdr-shortcut"
ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
MANIFEST="$ROOT_DIR/herdr-plugin.toml"
OUT_DIR="$ROOT_DIR/bin"
OUT_BIN="$OUT_DIR/$BIN_NAME"
MIN_GO_MINOR=26

log() { printf '%s\n' "herdr-shortcut build: $*" >&2; }
fail() { log "error: $*"; exit 1; }

[ -f "$MANIFEST" ] || fail "manifest not found: $MANIFEST"
VERSION="$(sed -n 's/^version[[:space:]]*=[[:space:]]*"\([^"]*\)".*$/\1/p' "$MANIFEST" | head -n1)"
# Require strict semantic version X.Y.Z.
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  fail "manifest version is not a strict semantic version (X.Y.Z): '$VERSION'"
fi

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Linux) GOOS=linux ;;
  Darwin) GOOS=darwin ;;
  *) fail "unsupported OS: $os (only Linux and macOS are supported)" ;;
esac
case "$arch" in
  x86_64 | amd64) GOARCH=amd64 ;;
  arm64 | aarch64) GOARCH=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac

mkdir -p "$OUT_DIR"

# Prefer building from the exact checkout when a compatible Go toolchain exists.
go_compatible() {
  command -v go >/dev/null 2>&1 || return 1
  # Inspect the toolchain already available on PATH. Without GOTOOLCHAIN=local,
  # an older Go can auto-download 1.26 merely while checking its version,
  # bypassing the smaller prebuilt-release fallback.
  gv="$(GOTOOLCHAIN=local go version 2>/dev/null | sed -n 's/.*go\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')"
  [ -n "$gv" ] || return 1
  gmajor="${gv%%.*}"
  gminor="${gv#*.}"
  [ "$gmajor" -gt 1 ] 2>/dev/null && return 0
  [ "$gmajor" -eq 1 ] 2>/dev/null && [ "$gminor" -ge "$MIN_GO_MINOR" ] 2>/dev/null && return 0
  return 1
}

if go_compatible; then
  log "building from source with $(go version)"
  ( cd "$ROOT_DIR" && GOTOOLCHAIN=local CGO_ENABLED=0 go build -trimpath -o "$OUT_BIN" ./cmd/herdr-shortcut )
  log "built $OUT_BIN"
  exit 0
fi

log "no compatible Go toolchain (need Go 1.$MIN_GO_MINOR+); downloading release v$VERSION"

TMP=""
cleanup() { [ -n "$TMP" ] && rm -rf "$TMP"; }
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM
TMP="$(mktemp -d)"

archive="${BIN_NAME}_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
# The release base URL is overridable only to point the no-Go fallback smoke test
# at locally generated assets; it defaults to the published GitHub release. The
# SHA-256 checksum is verified regardless of source, so this cannot install an
# unverified binary.
base_url="${HERDR_SHORTCUT_RELEASE_BASE_URL:-https://github.com/${REPO}/releases/download/v${VERSION}}"

download() { # url dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$1" -O "$2"
  else
    fail "curl or wget is required to download releases"
  fi
}

download "$base_url/$archive" "$TMP/$archive" || fail "could not download $archive"
download "$base_url/checksums.txt" "$TMP/checksums.txt" || fail "could not download checksums.txt"

expected="$(grep " ${archive}\$" "$TMP/checksums.txt" | awk '{print $1}' | head -n1)"
[ -n "$expected" ] || fail "no checksum listed for $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$TMP/$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$TMP/$archive" | awk '{print $1}')"
else
  fail "sha256sum or shasum is required to verify the download"
fi

[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive (expected $expected, got $actual)"
log "checksum verified"

tar -xzf "$TMP/$archive" -C "$TMP"
[ -f "$TMP/$BIN_NAME" ] || fail "release archive did not contain $BIN_NAME"
mv "$TMP/$BIN_NAME" "$OUT_BIN"
chmod +x "$OUT_BIN"
log "installed $OUT_BIN from release v$VERSION"
