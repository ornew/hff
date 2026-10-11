#!/bin/sh
# Run existing package tests on Go's js/wasm target with the Node.js executor.
set -eu
cd "$(dirname "$0")/.."

export GOTOOLCHAIN=go1.27.1
test "$(go env GOVERSION)" = go1.27.1
wasm_go_root=$(go env GOROOT)
wasm_node=$(command -v "${HFF_WASM_NODE:-node}") || {
  echo 'Node.js is required; add it to PATH or set HFF_WASM_NODE.' >&2
  exit 1
}
export HFF_WASM_NODE="$wasm_node" HFF_WASM_GOROOT="$wasm_go_root"
wasm_task_dir=$(mktemp -d "${TMPDIR:-/tmp}/hff-wasm-check.XXXXXX")
trap 'rm -rf "$wasm_task_dir"' EXIT HUP INT TERM
cat > "$wasm_task_dir/run-wasm" <<'EOF'
#!/bin/sh
exec "$HFF_WASM_NODE" --stack-size=8192 "$HFF_WASM_GOROOT/lib/wasm/wasm_exec_node.js" "$@"
EOF
chmod +x "$wasm_task_dir/run-wasm"

if [ "$#" -eq 0 ]; then
  set -- ./...
fi
GOOS=js GOARCH=wasm go test -exec "\"$wasm_task_dir/run-wasm\"" "$@"
