#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "Usage: $0 <darwin|linux> <amd64|arm64> <vX.Y.Z> <output-directory>" >&2
  exit 1
fi

target_os="$1"
target_arch="$2"
version="$3"
output_dir="$4"

case "$target_os/$target_arch" in
  darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
  *) echo "Unsupported workspace target: $target_os/$target_arch" >&2; exit 1 ;;
esac
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "Expected a workspace release version such as v0.1.0 or v0.2.0-rc.1" >&2
  exit 1
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/../.." && pwd)"
mkdir -p -- "$output_dir"
output_dir="$(cd -- "$output_dir" && pwd)"
staging_dir="$(mktemp -d "${TMPDIR:-/tmp}/agent-workspace-release.XXXXXX")"
trap 'rm -rf -- "$staging_dir"' EXIT

cd -- "$repo_root"
CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOWORK="$repo_root/go.work" \
  go build -trimpath -ldflags="-s -w" -o "$staging_dir/agent-engine" ./workspace/cmd/agent-engine
cp workspace/README.md workspace/README_cn.md "$staging_dir/"
archive="agent-engine-workspace_${version}_${target_os}_${target_arch}.tar.gz"
# Avoid macOS AppleDouble metadata entries when packaging locally.
COPYFILE_DISABLE=1 tar -czf "$output_dir/$archive" -C "$staging_dir" agent-engine README.md README_cn.md
echo "$output_dir/$archive"
