package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Compile before replacing the marked output directory, so a toolchain failure
// leaves the previous preview intact. Runtime and module use content hashes.
func compilePlayground(repo string) (map[string][]byte, error) {
	if runtime.Version() != "go1.27.1" {
		return nil, fmt.Errorf("playground build requires Go 1.27.1; got %s", runtime.Version())
	}
	dir, err := os.MkdirTemp("", "hff-site-wasm-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	file := filepath.Join(dir, "hff.wasm")
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-o", file, "./playground")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.1", "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build playground: %w\n%s", err, out)
	}
	wasm, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	runtimeJS, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		return nil, err
	}
	return playgroundFiles(wasm, runtimeJS)
}
func playgroundFiles(wasm, runtimeJS []byte) (map[string][]byte, error) {
	wasmName := fmt.Sprintf("hff.%x.wasm", sha256.Sum256(wasm))
	runtimeName := fmt.Sprintf("wasm_exec.%x.js", sha256.Sum256(runtimeJS))
	manifest, err := json.Marshal(map[string]string{"wasm": wasmName, "runtime": runtimeName})
	if err != nil {
		return nil, err
	}
	app, err := assets.ReadFile("playground.js")
	if err != nil {
		return nil, err
	}
	worker, err := assets.ReadFile("playground-worker.js")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"playground/" + wasmName: wasm, "playground/" + runtimeName: runtimeJS, "playground/manifest.json": manifest, "playground/app.js": app, "playground/worker.js": worker}, nil
}
