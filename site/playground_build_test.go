package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPlaygroundAssetIdentity(t *testing.T) {
	wasm, runtime := []byte("module"), []byte("runtime")
	files, err := playgroundFiles(wasm, runtime)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(files["playground/manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"wasm": fmt.Sprintf("hff.%x.wasm", sha256.Sum256(wasm)), "runtime": fmt.Sprintf("wasm_exec.%x.js", sha256.Sum256(runtime))} {
		if manifest[key] != want {
			t.Fatalf("%s: %s", key, manifest[key])
		}
		if len(files["playground/"+want]) == 0 {
			t.Fatalf("missing %s", want)
		}
	}
	changed, err := playgroundFiles([]byte("new module"), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if string(changed["playground/manifest.json"]) == string(files["playground/manifest.json"]) {
		t.Fatal("manifest did not invalidate old module")
	}
}

func TestPlaygroundWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("WASM worker integration")
	}
	node := os.Getenv("HFF_WASM_NODE")
	if node == "" {
		node = "node"
	}
	node, err := exec.LookPath(node)
	if err != nil {
		t.Skip("Node unavailable; set HFF_WASM_NODE")
	}
	for _, file := range []string{"testdata/playground-worker-smoke.mjs", "testdata/playground-worker-host.cjs"} {
		if _, err := os.ReadFile(file); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	files, err := compilePlayground(repo)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for file, data := range files {
		file = filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/playground-worker-smoke.mjs", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worker smoke: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
