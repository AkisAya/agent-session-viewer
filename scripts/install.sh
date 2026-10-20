#!/usr/bin/env bash
#
# Download and install the latest asv build for this platform.
#
#   curl -fsSL https://raw.githubusercontent.com/AkisAya/agent-session-viewer/main/scripts/install.sh | bash
#
# Env overrides:
#   VERSION=v0.5.0          install that tag instead of the latest stable release
#   ASV_INSTALL_DIR=/path   install elsewhere (default: /usr/local/bin if writable, else ~/.local/bin)
set -euo pipefail

repo=AkisAya/agent-session-viewer

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	MINGW* | MSYS* | CYGWIN*) os=windows ;;
	*)
		echo "unsupported OS: $(uname -s)" >&2
		exit 1
		;;
esac

case "$(uname -m)" in
	arm64 | aarch64) arch=arm64 ;;
	x86_64 | amd64) arch=amd64 ;;
	*)
		echo "unsupported arch: $(uname -m)" >&2
		exit 1
		;;
esac

# The release workflow only builds these, so fail before downloading a 404.
case "$os-$arch" in
	darwin-arm64 | darwin-amd64 | linux-amd64 | linux-arm64 | windows-amd64) ;;
	*)
		echo "no build for $os-$arch; see https://github.com/$repo/releases" >&2
		exit 1
		;;
esac

tag="${VERSION:-}"
if [ -z "$tag" ]; then
	# /releases/latest redirects to /releases/tag/<tag>; avoids the rate-limited API.
	latest=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
	tag=${latest##*/}
fi

if [ "$os" = windows ]; then
	asset="asv-$os-$arch-$tag.zip"
	bin=asv.exe
else
	asset="asv-$os-$arch-$tag.tar.gz"
	bin=asv
fi
url="https://github.com/$repo/releases/download/$tag/$asset"

dir="${ASV_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $url"
curl -fsSL "$url" -o "$tmp/$asset"
if [ "$os" = windows ]; then
	unzip -q "$tmp/$asset" -d "$tmp"
else
	tar -xzf "$tmp/$asset" -C "$tmp"
fi

chmod +x "$tmp/$bin"
mv -f "$tmp/$bin" "$dir/$bin"

echo "installed $dir/$bin ($tag)"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "note: $dir is not in PATH; add it to use asv directly" ;;
esac
