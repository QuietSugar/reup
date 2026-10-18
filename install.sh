#!/bin/sh
# reup installer — downloads the latest release binary.
#
#   curl -fsSL https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
#
# Behind a network that cannot reach GitHub, fetch this script through a mirror
# prefix and the asset downloads below reuse it automatically, e.g.
#   curl -fsSL https://gh-proxy.com/https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
#
# Env overrides:
#   RESUMER_INSTALL_DIR   target dir (default: ~/.local/bin)
#   RESUMER_VERSION       tag to install (default: latest, e.g. v0.2.0)
#   GH_PROXY              GitHub mirror prefix (e.g. https://gh-proxy.com/);
#                         empty = try GitHub directly, then the built-in mirrors
#   RESUMER_NO_MIRROR=1   disable the built-in mirror fallback (direct only)
#   https_proxy/http_proxy  standard proxy; curl honours it for script and assets
set -eu

REPO="QuietSugar/reup"
INSTALL_DIR="${RESUMER_INSTALL_DIR:-$HOME/.local/bin}"
DEFAULT_MIRRORS="https://gh-proxy.com/ https://ghfast.top/"

os="$(uname -s)"
case "$os" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) echo "error: unsupported OS: $os (darwin/linux only; Windows is on the roadmap)" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64) arch="amd64" ;;
  *) echo "error: unsupported architecture: $arch" >&2; exit 1 ;;
esac

# Mirror prefixes to fall back on. GH_PROXY wins; otherwise the built-in list is
# used unless RESUMER_NO_MIRROR=1. A mirror prefix is prepended verbatim to the
# full GitHub URL (https://<mirror>/https://github.com/...).
mirrors=""
if [ -n "${GH_PROXY:-}" ]; then
  mirrors="${GH_PROXY%/}/"
elif [ "${RESUMER_NO_MIRROR:-0}" != "1" ]; then
  mirrors="$DEFAULT_MIRRORS"
fi

# curl_get passes its arguments through to curl with sensible timeouts.
curl_get() {
  curl -fsSL --connect-timeout 10 --max-time 120 --retry 2 "$@"
}

# download <url> <dest>: try GitHub directly, then each mirror in turn.
download() {
  if curl_get -o "$2" "$1"; then
    return 0
  fi
  for m in $mirrors; do
    echo "note: direct download failed, retrying via $m" >&2
    curl_get -o "$2" "${m}$1" && return 0
  done
  return 1
}

# resolve_tag prints the latest release tag. The releases/latest redirect needs
# no API quota and works through a mirror prefix too.
resolve_tag() {
  url="https://github.com/$REPO/releases/latest"
  if out="$(curl_get -I -o /dev/null -w '%{url_effective}' "$url")"; then
    printf '%s\n' "$out" | sed 's#.*/tag/##'
    return 0
  fi
  for m in $mirrors; do
    echo "note: direct tag lookup failed, retrying via $m" >&2
    if out="$(curl_get -I -o /dev/null -w '%{url_effective}' "${m}$url")"; then
      printf '%s\n' "$out" | sed 's#.*/tag/##'
      return 0
    fi
  done
  return 1
}

tag="${RESUMER_VERSION:-}"
if [ -z "$tag" ]; then
  tag="$(resolve_tag || true)"
fi
if [ -z "$tag" ] || [ "$tag" = "latest" ]; then
  echo "error: could not resolve latest release tag (set GH_PROXY or https_proxy, or pass RESUMER_VERSION)" >&2
  exit 1
fi
version="${tag#v}"

archive="reup_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading reup $tag ($os/$arch)..."
if ! download "$base/$archive" "$tmp/$archive"; then
  echo "error: failed to download $archive (set GH_PROXY or https_proxy)" >&2
  exit 1
fi
if ! download "$base/checksums.txt" "$tmp/checksums.txt"; then
  echo "error: failed to download checksums.txt (set GH_PROXY or https_proxy)" >&2
  exit 1
fi

cd "$tmp"
# Extract the expected line first: a grep miss inside a pipe would otherwise
# feed the checker empty stdin and could pass vacuously (sh has no pipefail).
expected="$(grep " $archive\$" checksums.txt || true)"
if [ -z "$expected" ]; then
  echo "error: $archive not found in checksums.txt — aborting" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  printf '%s\n' "$expected" | sha256sum -c - >/dev/null
else
  printf '%s\n' "$expected" | shasum -a 256 -c - >/dev/null
fi
echo "Checksum OK."

tar -xzf "$archive"
mkdir -p "$INSTALL_DIR"
install -m 0755 reup "$INSTALL_DIR/reup"

echo "Installed: $INSTALL_DIR/reup"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "note: $INSTALL_DIR is not on your PATH. Add this to your shell rc:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

"$INSTALL_DIR/reup" --version
