#!/bin/sh
# tn5250-tui installer for Linux and macOS.
#   curl -fsSL https://raw.githubusercontent.com/DerKyberTakt/tn5250-tui/main/install.sh | sh
# Environment:
#   TN5250_TUI_VERSION  pin a release (for example v0.1.0); default = latest
#   PREFIX              install into $PREFIX/bin (default: /usr/local/bin as root, else ~/.local/bin)
set -eu

REPO="DerKyberTakt/tn5250-tui"
BIN="tn5250-tui"

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# fetch URL [OUTFILE]: curl or wget; no OUTFILE = stdout
fetch() {
    if have curl; then
        if [ $# -ge 2 ]; then curl -fsSL -o "$2" "$1"; else curl -fsSL "$1"; fi
    elif have wget; then
        if [ $# -ge 2 ]; then wget -q -O "$2" "$1"; else wget -q -O - "$1"; fi
    else
        die "need curl or wget"
    fi
}

# --- platform ---------------------------------------------------------------
case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS: $(uname -s) (Linux and macOS only; Windows: use install.ps1)" ;;
esac
case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported CPU: $(uname -m)" ;;
esac
if [ "$os" = darwin ] && [ "$arch" != arm64 ]; then
    die "no macOS $arch build is published (darwin_arm64 only)"
fi

# --- version ----------------------------------------------------------------
tag="${TN5250_TUI_VERSION:-}"
if [ -z "$tag" ]; then
    say "Looking up the latest release ..."
    tag=$(fetch "https://api.github.com/repos/$REPO/releases/latest" |
        sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
    [ -n "$tag" ] || die "could not find the latest release tag (no release published yet?)"
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
ver="${tag#v}"

asset="${BIN}_${ver}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

# --- target directory -------------------------------------------------------
if [ -n "${PREFIX:-}" ]; then
    dest="$PREFIX/bin"
elif [ "$(id -u)" -eq 0 ]; then
    dest=/usr/local/bin
else
    dest="$HOME/.local/bin"
fi

# --- download + verify ------------------------------------------------------
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t tn5250tui)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $asset ($tag) ..."
fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "download failed: $base/SHA256SUMS"

want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "$asset is not listed in SHA256SUMS"
if have sha256sum; then
    got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
elif have shasum; then
    got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
else
    die "need sha256sum or shasum to verify the download"
fi
[ "$want" = "$got" ] || die "checksum mismatch for $asset (wanted $want, got $got)"
say "Checksum OK."

# --- install ----------------------------------------------------------------
tar -xzf "$tmp/$asset" -C "$tmp" "$BIN" || die "$BIN not found inside $asset"
mkdir -p "$dest" || die "cannot create $dest (set PREFIX or run as root)"
if have install; then
    install -m 755 "$tmp/$BIN" "$dest/$BIN"
else
    cp "$tmp/$BIN" "$dest/$BIN"
    chmod 755 "$dest/$BIN"
fi
say "Installed $dest/$BIN ($tag)."

case ":$PATH:" in
    *":$dest:"*) ;;
    *)
        say ""
        say "$dest is not in your PATH. Add it, for example:"
        say "  echo 'export PATH=\"$dest:\$PATH\"' >> ~/.profile"
        say "and open a new shell (or run: export PATH=\"$dest:\$PATH\")."
        ;;
esac
say ""
say "Run: $BIN   (or: $BIN <host>[:port])"
