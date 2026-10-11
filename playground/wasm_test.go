//go:build !(js && wasm)

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWASMBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("WASM build smoke test")
	}
	// Register the external fixture with Go's test cache as well as Node.
	if _, err := os.ReadFile("testdata/smoke.mjs"); err != nil {
		t.Fatal(err)
	}
	node := os.Getenv("HFF_WASM_NODE")
	if node == "" {
		node = "node"
	}
	node, err := exec.LookPath(node)
	if err != nil {
		t.Skip("Node.js unavailable; set HFF_WASM_NODE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := t.TempDir()
	wasm := filepath.Join(dir, "hff.wasm")
	cli := filepath.Join(dir, "hff-playground")
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	for _, target := range []struct {
		output string
		wasm   bool
	}{{cli, false}, {wasm, true}} {
		cmd := exec.CommandContext(ctx, goTool, "build", "-o", target.output, ".")
		cmd.Env = os.Environ()
		if target.wasm {
			cmd.Env = append(cmd.Env, "GOOS=js", "GOARCH=wasm")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
	}
	cmd := exec.CommandContext(ctx, node, "testdata/smoke.mjs", wasm, filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js"), cli)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("WASM smoke: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
