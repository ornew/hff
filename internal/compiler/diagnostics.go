package compiler

import (
	"strings"

	"github.com/ornew/hff/internal/core"
)

// Keep diagnostics concise even for deeply nested service schemas.
func describeType(t core.Type) string { return describeTypeDepth(t, 0) }
func describeTypeDepth(t core.Type, depth int) string {
	if depth >= 4 {
		return "..."
	}
	switch t.Kind() {
	case core.UnionType:
		members := t.Members()
		var names []string
		for i, member := range members {
			if i == 6 {
				names = append(names, "...")
				break
			}
			names = append(names, describeTypeDepth(member, depth+1))
		}
		return strings.Join(names, "|")
	case core.ListType:
		return "list[" + describeTypeDepth(t.Element(), depth+1) + "]"
	case core.MapType:
		return "map[" + describeTypeDepth(t.Element(), depth+1) + "]"
	case core.StructType:
		return "object"
	}
	names := map[core.TypeKind]string{core.InvalidType: "invalid", core.NullType: "null", core.BoolType: "bool",
		core.IntType: "int", core.FloatType: "float", core.StringType: "string", core.BytesType: "bytes",
		core.TimestampType: "timestamp", core.DurationType: "duration", core.AnyType: "any", core.NeverType: "never", core.FunctionType: "callback"}
	return names[t.Kind()]
}
