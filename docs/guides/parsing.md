# Parse HFF Syntax During Development

This example shows the repository's syntax-only parser. It turns source text
into a typed syntax tree and reports source locations for malformed input. It
does not resolve names against a schema, check types or enabled capabilities,
or decide whether a query depends on service data. Those checks belong to the
future common `Compile` stage.

The parser currently lives in the module-internal package
`internal/syntax`. The example is runnable from this repository, but Go's
`internal` package rule prevents applications outside this module from
importing it. HFF does not currently expose a public parse-only API.

## Parse a few expressions

The example parses ordinary predicates, a callback, an empty filter, and two
invalid inputs:

```go
package main

import (
    "context"
    "errors"
    "fmt"

    "github.com/ornew/hff"
    "github.com/ornew/hff/internal/syntax"
)

func main() {
    examples := []string{
        `name="Alice" age>=18`,
        `items:any(x=>x.price*x.quantity>=100)`,
        `profile!.email~"@example\\.com$"`,
        ``,
        `age > 10`, // Comparison operators cannot have surrounding spaces.
        `name="unterminated`,
    }

    for _, source := range examples {
        tree, err := syntax.ParseQuery(context.Background(), source, syntax.Config{})
        if err != nil {
            var detail *hff.Error
            if errors.As(err, &detail) && detail.Span != nil {
                fmt.Printf("rejected at %d:%d: %s\n", detail.Line, detail.Column, detail.Message)
            } else {
                fmt.Printf("rejected: %v\n", err)
            }
            continue
        }
        span := syntax.ExprSpan(tree)
        fmt.Printf("parsed %T at bytes [%d,%d): %q\n", tree, span.Start, span.End, source)
    }
}
```

Save the program as `examples/parsing/main.go` or use the checked-in example,
then run it from the repository root:

```sh
go run ./examples/parsing
```

The example accepts any syntactically valid variable name. Parsing does not
prove that a name exists in a service schema. For example, `unknown>0` parses
successfully; a future `Compile` call will resolve names, validate types and
capabilities, enforce the filter-dependence rule, and produce a reusable
program. A syntax tree is not yet a compiled filter and cannot be evaluated or
translated to SQL.

## Syntax limits and cancellation

`syntax.ParseQuery` accepts a context and parser configuration. Zero limit fields
select the finite defaults; `hff.Limits.MaxQueryBytes` limits UTF-8 source bytes
and `MaxSyntaxDepth` limits meaningful expression nesting. The current default
for each is 65,536 bytes and 64 levels. The root expression is depth zero.

Depth counts parentheses, operators, collection nesting, guards, access parts,
and callback bodies. Singleton logical wrappers and access nodes with no suffix
do not add depth. The parser checks query size before parsing and checks the
resulting typed tree afterward; a preflight scan bounds delimiter, guard, and
unary-operator runs before entering the generated parser. The generated parser
also retains an internal rule-call safety limit.

Cancellation is checked before parsing, periodically during the preflight scan,
after generated parsing, and while validating the resulting tree. The generated
`ParseAST` call has no cooperative context hook, so cancellation that occurs
during that call is reported after parsing returns rather than interrupting it
immediately.

Syntax diagnostics report a UTF-8 byte span and a one-based line and Unicode
code-point column. Invalid UTF-8, malformed strings (including unpaired
surrogate escapes), and malformed syntax are rejected. A completed syntax parse
only confirms that the source follows the grammar.
