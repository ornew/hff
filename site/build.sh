#!/bin/sh
# Build repository Markdown as a static site, optionally serving a local preview.
set -eu
cd "$(dirname "$0")"
exec go run . -repo .. -out dist "$@"
