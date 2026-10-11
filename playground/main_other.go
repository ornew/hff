//go:build !(js && wasm)

package main

import (
	"context"
	"io"
	"os"
)

func main() {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxRequestBytes+1))
	if err != nil {
		os.Exit(1)
	}
	os.Stdout.Write(handle(context.Background(), raw))
	os.Stdout.Write([]byte("\n"))
}
