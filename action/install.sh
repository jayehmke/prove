#!/usr/bin/env bash
# Puts prove on PATH: the prove already there when no version is asked
# for, else a release archive checked against the release's checksums.txt.
# Perl ships a different `prove` (/usr/bin/prove on Ubuntu runners), so a
# prove on PATH counts only if `prove version` answers "prove ...".
set -euo pipefail

version="${PROVE_VERSION:-}"
if [ -z "$version" ] && prove version 2>/dev/null | grep -q '^prove '; then
  echo "prove: using $(command -v prove)"
  exit 0
fi

releases="${PROVE_RELEASES:-https://github.com/jayehmke/prove/releases}"
if [ -n "$version" ]; then
  url="$releases/download/v${version#v}"
else
  url="$releases/latest/download"
fi
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*)
  echo "::error::prove: no release for $(uname -m)"
  exit 1
  ;;
esac
asset="prove_${os}_${arch}.tar.gz"

dir="${RUNNER_TEMP:-$(mktemp -d)}/prove-bin"
mkdir -p "$dir"
curl -fsSL -o "$dir/$asset" "$url/$asset"
curl -fsSL -o "$dir/checksums.txt" "$url/checksums.txt"
want=$(awk -v a="$asset" '$2 == a { print $1 }' "$dir/checksums.txt")
got=$({ sha256sum "$dir/$asset" 2>/dev/null || shasum -a 256 "$dir/$asset"; } | awk '{ print $1 }')
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "::error::prove: $asset does not match checksums.txt from $url"
  exit 1
fi
tar -xzf "$dir/$asset" -C "$dir"
echo "prove: installed $("$dir/prove" version) from $url"
echo "$dir" >>"${GITHUB_PATH:?}"
