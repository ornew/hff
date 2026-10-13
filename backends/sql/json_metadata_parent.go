package sql

import "github.com/ornew/hff"

// Reuse only a parent already restored in this projection scope. The most
// specific physical binding has been selected before entering this function;
// guards, callback scopes and order-demand classes retain their own caches.
func (b *builder) restoredMetadataParent(n hff.Node, boundDepth int) (expression, bool, error) {
	if len(b.sharedPaths) == 0 {
		return expression{}, false, nil
	}
	path, _ := n.Path()
	parent, _ := n.Child(0)
	// metadataSubpath has established an unguarded schema-rooted path.
	// Construct its ordinary cache key directly without rewalking each prefix.
	for depth := len(path) - 1; depth >= boundDepth; depth-- {
		key := "schema:" + pathKey(path[:depth]) + ":" + parent.Type().Key() + b.pathOrderClass(parent)
		if value, found := b.sharedPaths[key]; found && b.jsonValues[value.code] == JSON {
			code := value.code
			for _, field := range path[depth:] {
				code = b.project(code)
				key, err := b.parameter(n, hff.String(field))
				if err != nil {
					return expression{}, true, err
				}
				code = "(CASE WHEN json_typeof(" + code + ")='object' THEN " + code + " -> " + key.code + "::text ELSE NULL END)"
			}
			result, err := b.jsonDecode(n, code, JSON, b.jsonStoredType(n), value.present)
			return result, true, err
		}
		parent, _ = parent.Child(0)
	}
	return expression{}, false, nil
}
