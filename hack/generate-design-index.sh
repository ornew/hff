#!/bin/sh
# Keep the GitHub introduction and generated site index in sync.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=go1.27.1
hff_index_tmp=$(mktemp docs/design/.index.XXXXXX)
trap 'rm -f "$hff_index_tmp"' EXIT HUP INT TERM
(
  cd site
  go run . -repo .. -design-index
) > "$hff_index_tmp"
chmod 644 "$hff_index_tmp"
mv "$hff_index_tmp" docs/design/README.md
