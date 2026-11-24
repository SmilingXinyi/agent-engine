#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/agent-engine-install-test.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
case "$(uname -s)" in Darwin) platform=darwin ;; Linux) platform=linux ;; *) exit 1 ;; esac
case "$(uname -m)" in arm64|aarch64) architecture=arm64 ;; x86_64|amd64) architecture=amd64 ;; *) exit 1 ;; esac
archive=agent-engine-cli_v1.2.3_${platform}_${architecture}.tar.gz

mkdir "$scratch/bin" "$scratch/assets" "$scratch/payload"
cat > "$scratch/bin/curl" <<'CURL'
#!/bin/sh
set -eu
if [ "$1" = -fsSL ]; then
  cat "$INSTALL_TEST_ASSETS/${2##*/}"
  exit
fi
while [ "$1" != --output ]; do shift; done
cp "$INSTALL_TEST_ASSETS/${3##*/}" "$2"
CURL
chmod +x "$scratch/bin/curl"
printf '#!/bin/sh\nprintf "installed fixture\\n"\n' > "$scratch/payload/agent-engine"
COPYFILE_DISABLE=1 tar -czf "$scratch/assets/$archive" -C "$scratch/payload" agent-engine
(
  cd "$scratch/assets"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "./$archive" > SHA256SUMS
  else
    shasum -a 256 "./$archive" > SHA256SUMS
  fi
)
export INSTALL_TEST_ASSETS="$scratch/assets"
PATH="$scratch/bin:$PATH"
export PATH
cp "$script_dir/install.sh" "$scratch/assets/install.sh"
curl -fsSL https://github.com/SmilingXinyi/agent-engine/releases/latest/download/install.sh | sh -s -- --install-dir "$scratch/install path"
[ "$("$scratch/install path/agent-engine")" = "installed fixture" ]

# A failed verification must leave the previously installed executable intact.
printf 'tampered content\n' > "$scratch/assets/$archive"
if sh "$script_dir/install.sh" --install-dir "$scratch/install path" > "$scratch/install.log" 2>&1; then
  printf '%s\n' 'installer accepted a modified archive' >&2
  exit 1
fi
case "$(cat "$scratch/install.log")" in *'checksum does not match'*) ;; *) cat "$scratch/install.log" >&2; exit 1 ;; esac
[ "$("$scratch/install path/agent-engine")" = "installed fixture" ]
