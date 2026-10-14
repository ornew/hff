package sql

import (
	"strconv"

	"github.com/ornew/hff"
)

// parsedJSONRoot recognizes unguarded literal-field chains of the built-in
// parser. Guards, indexing, extensions, and arbitrary computed expressions
// retain their existing lowering rather than acquiring a new reuse contract.
func parsedJSONRoot(n hff.Node) bool {
	for n.Kind() == hff.FieldNode {
		n, _ = n.Child(0)
	}
	method, ok := n.Method()
	if !ok || method.Name() != "fromjson" {
		return false
	}
	for _, signature := range method.Signatures() {
		if _, custom := signature.Declaration(); custom {
			return false
		}
	}
	return true
}

// The raw decoded expression contains the cached validated parse alias or its
// cached field alias. Scope resets discard these keys before entering another
// row source; presence never leaks between guarded and ordinary receivers.
func (b *builder) shareableParsedValue(n hff.Node, value expression) (string, bool) {
	if !parsedJSONRoot(n) {
		return "", false
	}
	format := b.jsonValues[value.code]
	return "parsed-json-value:" + pathKey([]string{value.code, value.present, strconv.Itoa(int(value.kind)), string(format), n.Type().Key(), b.jsonStoredType(n).Key(), b.pathOrderClass(n)}), true
}
