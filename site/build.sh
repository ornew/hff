#!/bin/sh
# Build repository Markdown as a static site, optionally serving a local preview.
set -eu
export GOTOOLCHAIN=go1.27.1
cd "$(dirname "$0")"
exec go run . -repo .. -out dist "$@"
