#!/bin/sh
# Installs oci-assume from a GitHub release after verifying its checksum.
# Usage: install.sh [--version vX.Y.Z]
# Env: OCI_ASSUME_INSTALL_DIR (default ~/.local/bin), OCI_ASSUME_BASE_URL (test override)
set -eu

REPO="123yogin/oci-assume"

die() { echo "install.sh: $*" >&2; exit 1; }

main() {
  version=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
      --version=*) version=${1#--version=}; shift ;;
      -h|--help) echo "usage: install.sh [--version vX.Y.Z]"; exit 0 ;;
      *) die "unknown argument: $1" ;;
    esac
  done

  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS: $(uname -s) (Windows: use scoop or winget)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac

  command -v curl >/dev/null 2>&1 || die "curl is required"
  if [ -n "${OCI_ASSUME_BASE_URL:-}" ]; then
    set -- -fsSL
  else
    set -- --proto '=https' --tlsv1.2 -fsSL
  fi

  if [ -z "$version" ]; then
    version=$(curl "$@" "https://api.github.com/repos/$REPO/releases/latest" |
      sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
    [ -n "$version" ] || die "could not resolve the latest release"
  fi
  case "$version" in v*) ;; *) version="v$version" ;; esac
  case "$version" in */*) die "invalid version: $version" ;; esac
  case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) die "invalid version: $version (want vX.Y.Z)" ;; esac

  # Must match name_template in .goreleaser.yaml:
  #   oci-assume_{{ .Version }}_{{ .Os }}_{{ .Arch }}.tar.gz  (Version has no leading v)
  archive="oci-assume_${version#v}_${os}_${arch}.tar.gz"
  base=${OCI_ASSUME_BASE_URL:-https://github.com/$REPO/releases/download/$version}

  tmp=$(mktemp -d) || die "mktemp failed"
  trap 'rm -rf "$tmp"' EXIT
  trap 'exit 130' INT TERM

  echo "Downloading $archive ($version)" >&2
  curl "$@" -o "$tmp/$archive" "$base/$archive" || die "download failed: $base/$archive"
  curl "$@" -o "$tmp/checksums.txt" "$base/checksums.txt" || die "download failed: $base/checksums.txt"

  want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/checksums.txt")
  [ -n "$want" ] || die "no checksum entry for $archive; aborting"

  if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
  elif command -v shasum >/dev/null 2>&1; then
    got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
  else
    die "need sha256sum or shasum to verify the download"
  fi
  [ "$got" = "$want" ] || die "checksum mismatch for $archive (want $want, got $got); aborting"

  mkdir "$tmp/x"
  tar -xzf "$tmp/$archive" -C "$tmp/x" oci-assume || die "archive has no oci-assume binary"

  dest=${OCI_ASSUME_INSTALL_DIR:-$HOME/.local/bin}
  mkdir -p "$dest"
  cp "$tmp/x/oci-assume" "$dest/oci-assume.new"
  chmod 755 "$dest/oci-assume.new"
  mv -f "$dest/oci-assume.new" "$dest/oci-assume"
  echo "Installed oci-assume $version to $dest/oci-assume" >&2

  case ":$PATH:" in
    *":$dest:"*) ;;
    *) echo "Add $dest to your PATH, e.g.: export PATH=\"$dest:\$PATH\"" >&2 ;;
  esac
}

main "$@"
