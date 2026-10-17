#!/usr/bin/env bash
#
# Assembles the browser harness into dist/.
#
# The result has to be served over HTTP: the page fetches app.wasm and
# golden.txt, which file:// will not allow.
#
#   ./build.sh
#   python3 -m http.server -d dist 8000
#   open http://localhost:8000/

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
dist="$here/dist"

mkdir -p "$dist"
cd "$root"

GOOS=js GOARCH=wasm go build -o "$dist/app.wasm" ./cmd/wasmcheck
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$dist/wasm_exec.js"
cp "$here/../testdata/golden.txt" "$dist/golden.txt"
cp "$here/index.html" "$here/vfs.js" "$dist/"

printf 'built %s\n' "$dist"
printf 'serve it with:  python3 -m http.server -d %s 8000\n' "$dist"
