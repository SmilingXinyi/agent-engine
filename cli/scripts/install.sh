#!/bin/sh
set -eu

usage() { printf '%s\n' 'Usage: install.sh [--install-dir DIRECTORY]'; }
die() { printf 'agent-engine installer: %s\n' "$*" >&2; exit 1; }

install_dir=
case "$#" in
  0) ;;
  1) case "$1" in -h|--help) usage; exit 0 ;; *) usage >&2; exit 1 ;; esac ;;
  2) [ "$1" = --install-dir ] && [ -n "$2" ] || die 'expected --install-dir DIRECTORY'
     install_dir=$2 ;;
  *) usage >&2; exit 1 ;;
esac

case "$(uname -s)" in
  Darwin) platform=darwin ;;
  Linux) platform=linux ;;
  *) die 'supported operating systems are macOS and Linux' ;;
esac
case "$(uname -m)" in
  x86_64|amd64) architecture=amd64 ;;
  arm64|aarch64) architecture=arm64 ;;
  *) die 'supported architectures are amd64 and arm64' ;;
esac

if [ -z "$install_dir" ]; then
  [ -n "${HOME:-}" ] || die 'set HOME or specify --install-dir'
  install_dir=$HOME/.local/bin
fi
case "$install_dir" in /*) ;; *) install_dir=$PWD/$install_dir ;; esac

command -v curl >/dev/null 2>&1 || die 'curl is required'
mkdir -p "$install_dir"
destination=$install_dir/agent-engine
[ ! -d "$destination" ] || die "$destination is a directory"
scratch=$(mktemp -d "$install_dir/.agent-engine-install.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

release=https://github.com/SmilingXinyi/agent-engine/releases/latest/download
download() {
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --output "$2" "$release/$1"
}

download SHA256SUMS "$scratch/SHA256SUMS"
entry=$(awk -v target="_${platform}_${architecture}" '
  {
    archive = $2
    sub(/^\.\//, "", archive)
    if (archive ~ ("^agent-engine-cli_v[0-9]+[.][0-9]+[.][0-9]+" target "[.]tar[.]gz$") &&
        length($1) == 64 && $1 !~ /[^0-9a-f]/) {
      print $1, archive
      found++
    }
  }
  END { if (found != 1) exit 1 }
' "$scratch/SHA256SUMS") || die 'release checksum list must contain one archive for this platform'
expected=${entry%% *}
archive=${entry#* }

download "$archive" "$scratch/$archive"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum < "$scratch/$archive")
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 < "$scratch/$archive")
else
  die 'sha256sum or shasum is required'
fi
[ "${actual%% *}" = "$expected" ] || die 'download checksum does not match SHA256SUMS'

[ "$(tar -tzf "$scratch/$archive")" = agent-engine ] || die 'release archive must contain only agent-engine'
tar -xOzf "$scratch/$archive" agent-engine > "$scratch/agent-engine"
[ -s "$scratch/agent-engine" ] || die 'release archive contained an empty binary'
chmod 0755 "$scratch/agent-engine"
mv -f "$scratch/agent-engine" "$destination"
printf 'Installed %s\n' "$destination"
