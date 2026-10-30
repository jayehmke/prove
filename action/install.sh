#!/usr/bin/env bash
# Puts prove on PATH: the release named by PROVE_VERSION, by default the one
# this action pins in action/VERSION (never "latest"). The archive must
# match the release's checksums.txt and carry a GitHub build attestation
# from Feral-Intelligence/prove's release workflow for that tag, so a tampered release
# whose checksums.txt was rewritten to match still fails.
#
# A prove already on PATH is used only when PROVE_FROM_PATH=true: on a
# persistent runner an earlier job could have planted one. Perl ships a
# different `prove` (/usr/bin/prove on Ubuntu runners), so even then it
# counts only if `prove version` answers "prove ...".
set -euo pipefail

if [ "${PROVE_FROM_PATH:-false}" = true ] && prove version 2>/dev/null | grep -q '^prove '; then
  echo "prove: using $(command -v prove) from PATH (from-path: true)"
  exit 0
fi

version="${PROVE_VERSION:-}"
if [ -z "$version" ]; then
  version=$(tr -d '[:space:]' <"$(dirname "$0")/VERSION")
fi
version="${version#v}"
if ! printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  echo "::error::prove: version must be an exact release such as 1.2.0, not '$version'"
  exit 1
fi

repo=Feral-Intelligence/prove
releases="${PROVE_RELEASES:-https://github.com/$repo/releases}"
url="$releases/download/v$version"
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
if ! command -v gh >/dev/null; then
  echo "::error::prove: the GitHub CLI (gh) is needed to verify the release attestation of $asset"
  exit 1
fi
if ! gh attestation verify "$dir/$asset" --repo "$repo" \
  --signer-workflow "$repo/.github/workflows/release.yml" \
  --source-ref "refs/tags/v$version" --deny-self-hosted-runners; then
  echo "::error::prove: $asset has no build attestation from $repo's release workflow for v$version"
  exit 1
fi
tar -xzf "$dir/$asset" -C "$dir"
echo "prove: installed $("$dir/prove" version) from $url"
echo "$dir" >>"${GITHUB_PATH:?}"
