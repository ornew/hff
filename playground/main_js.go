//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"syscall/js"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("match", js.FuncOf(func(_ js.Value, args []js.Value) any {
		var raw string
		if len(args) == 1 && args[0].Type() == js.TypeString {
			// UTF-16 length is a lower bound on UTF-8 bytes. Reject grossly
			// oversized messages before allocating their Go string copy.
			if js.Global().Get("Object").Invoke(args[0]).Get("length").Int() > maxRequestBytes {
				return `{"ok":false,"error":{"stage":"request","code":"limit","message":"playground JSON exceeds 1 MiB"}}`
			}
			raw = args[0].String()
		}
		return string(handle(context.Background(), []byte(raw)))
	}))
	api.Set("sqlExamples", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		examples, err := sqlExamples()
		if err != nil {
			return `{"error":"SQL examples are unavailable"}`
		}
		data, err := json.Marshal(examples)
		if err != nil {
			return `{"error":"SQL examples are unavailable"}`
		}
		return string(data)
	}))
	js.Global().Set("hffPlayground", api)
	if ready := js.Global().Get("hffPlaygroundReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {}
}
