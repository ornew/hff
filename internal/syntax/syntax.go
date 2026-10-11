// Package syntax implements HFF's fixed grammar using a PEGO-generated typed parser.
// It checks syntax only; schema, type, capability, and field-dependence checks
// belong to common compilation. The generated AST is internal mutable storage.
package syntax

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
)

type Config struct {
	Limits    hff.Limits
	Unlimited bool
}

// ParseQuery returns a complete typed syntax tree with UTF-8 byte spans.
func ParseQuery(ctx context.Context, source string, config Config) (Expr, error) {
	if ctx == nil {
		return nil, &hff.Error{Code: hff.ConfigurationError, Message: "nil context"}
	}
	limits, err := config.Limits.Resolve()
	if err != nil {
		return nil, &hff.Error{Code: hff.ConfigurationError, Message: err.Error()}
	}
	if config.Unlimited {
		limits = hff.Limits{}
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if limits.MaxQueryBytes > 0 && len(source) > limits.MaxQueryBytes {
		return nil, sourceError(hff.LimitError, source, 0, "query byte limit exceeded")
	}
	validUTF8, err := core.ValidUTF8(ctx, source)
	if err != nil {
		return nil, err
	}
	if !validUTF8 {
		nextCheck := 0
		for i := 0; i < len(source); {
			if i >= nextCheck {
				if err := checkContext(ctx); err != nil {
					return nil, err
				}
				nextCheck = i + 1024
			}
			_, width := utf8.DecodeRuneInString(source[i:])
			if width == 1 && source[i] >= 0x80 {
				return nil, sourceError(hff.SyntaxError, source, i, "query is not valid UTF-8")
			}
			i += width
		}
	}
	if err := preflight(ctx, source, limits.MaxSyntaxDepth); err != nil {
		return nil, err
	}
	tree, err := ParseAST(source, Bytes)
	if contextErr := checkContext(ctx); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		var syntaxErr *SyntaxError
		if errors.As(err, &syntaxErr) {
			return nil, sourceError(hff.SyntaxError, source, syntaxErr.Pos, syntaxErr.Message())
		}
		if strings.Contains(err.Error(), "nesting too deep") {
			return nil, sourceError(hff.LimitError, source, 0, "generated parser nesting limit exceeded")
		}
		return nil, &hff.Error{Code: hff.SyntaxError, Message: err.Error(), Cause: err}
	}
	if err := checkTree(ctx, source, tree, limits.MaxSyntaxDepth); err != nil {
		return nil, err
	}
	return tree, nil
}
func checkContext(ctx context.Context) error {
	return core.CheckContext(ctx)
}
func sourceError(code hff.ErrorCode, source string, pos int, message string) error {
	if pos < 0 {
		pos = 0
	}
	if pos > len(source) {
		pos = len(source)
	}
	end := pos
	if pos < len(source) {
		_, width := utf8.DecodeRuneInString(source[pos:])
		end += width
	}
	line := strings.Count(source[:pos], "\n") + 1
	start := strings.LastIndexByte(source[:pos], '\n') + 1
	column := utf8.RuneCountInString(source[start:pos]) + 1
	return &hff.Error{Code: code, Span: &hff.Span{Start: pos, End: end}, Line: line, Column: column, Message: message}
}

// preflight bounds recursive delimiters and consecutive guards/unary operators
// before entering the generated parser. The typed-tree check enforces full depth.
func preflight(ctx context.Context, source string, limit int) error {
	depth, run, nextCheck := 0, 0, 0
	var quote byte
	for i := 0; i < len(source); i++ {
		if i >= nextCheck {
			if err := checkContext(ctx); err != nil {
				return err
			}
			nextCheck = i + 1024
		}
		c := source[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '`' {
			quote = c
			run = 0
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
			run = 0
		case ')', ']', '}':
			depth--
			run = 0
		case '!', '-':
			run++
		default:
			run = 0
		}
		if limit > 0 && depth+run > limit {
			return sourceError(hff.LimitError, source, i, "syntax depth limit exceeded")
		}
	}
	return nil
}

// DecodeString rejects invalid Unicode without replacing it.
func DecodeString(raw string) (string, error) { return core.DecodeJSONString(raw) }
func DecodeName(name Name) string {
	switch n := name.(type) {
	case *PlainName:
		return n.Text
	case *QuotedName:
		return strings.NewReplacer("\\`", "`", "\\\\", "\\").Replace(n.Text[1 : len(n.Text)-1])
	}
	return ""
}
func ExprSpan(node any) hff.Span {
	if spanned, ok := node.(interface{ tspan() (int, int) }); ok {
		start, end := spanned.tspan()
		return hff.Span{Start: start, End: end}
	}
	return hff.Span{}
}

// checkTree is iterative, so long arithmetic/access chains do not exhaust the Go stack.
func checkTree(ctx context.Context, source string, tree Expr, limit int) error {
	type entry struct {
		node  any
		depth int
	}
	stack := []entry{{tree, 0}}
	for len(stack) > 0 {
		if err := checkContext(ctx); err != nil {
			return err
		}
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if limit > 0 && next.depth > limit {
			return sourceError(hff.LimitError, source, ExprSpan(next.node).Start, "syntax depth limit exceeded")
		}
		push := func(n any, depth int) { stack = append(stack, entry{n, depth}) }
		childDepth := next.depth + 1
		switch n := next.node.(type) {
		case *Or:
			if len(n.Terms) == 1 {
				childDepth = next.depth
			}
			for i := len(n.Terms) - 1; i >= 0; i-- {
				push(n.Terms[i], childDepth)
			}
		case *And:
			if len(n.Terms) == 1 {
				childDepth = next.depth
			}
			for i := len(n.Terms) - 1; i >= 0; i-- {
				push(n.Terms[i], childDepth)
			}
		case *Access:
			push(n.Base, next.depth+len(n.Parts))
			for i, part := range n.Parts {
				switch p := part.(type) {
				case *IndexAccess:
					push(p.Index, next.depth+len(n.Parts)-i)
				case *MethodCall:
					for j := len(p.Args) - 1; j >= 0; j-- {
						push(p.Args[j], next.depth+len(n.Parts)-i)
					}
				case *TypeGuard:
					push(p.Type, next.depth+len(n.Parts)-i)
				}
			}
		case *Not:
			push(n.X, childDepth)
		case *Unary:
			push(n.X, childDepth)
		case *Binary:
			push(n.Right, childDepth)
			push(n.Left, childDepth)
		case *Group:
			push(n.X, childDepth)
		case *ListLiteral:
			for i := len(n.Values) - 1; i >= 0; i-- {
				push(n.Values[i], childDepth)
			}
		case *ObjectLiteral:
			for i := len(n.Members) - 1; i >= 0; i-- {
				member := n.Members[i]
				if _, err := DecodeString(member.Key.Text); err != nil {
					return sourceError(hff.SyntaxError, source, member.Key.Start, err.Error())
				}
				push(member.Value, childDepth)
			}
		case *Callback:
			push(n.Body, childDepth)
		case *TypeUnion:
			if len(n.Types) == 1 {
				childDepth = next.depth
			}
			for _, typ := range n.Types {
				push(typ, childDepth)
			}
		case *TypeNamed:
			for _, typ := range n.Args {
				push(typ, childDepth)
			}
		case *TypeGroup:
			push(n.X, childDepth)
		case *String:
			if _, err := DecodeString(n.Text); err != nil {
				return sourceError(hff.SyntaxError, source, n.Start, err.Error())
			}
		}
	}
	return nil
}
