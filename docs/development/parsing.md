# Parse HFF Syntax During Development

This example shows the repository's syntax-only parser. It turns source text
into a typed syntax tree and reports source locations for malformed input. It
does not resolve names against a schema, check types or enabled capabilities,
or decide whether a query depends on service data. Those checks belong to
`engine.Compile`; see the [compilation guide](../guides/compiling.md).

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
successfully; `engine.Compile` resolves names, validates types and capabilities,
enforces the filter-dependence rule, and produces a reusable typed program. A
syntax tree is not an `hff.Program` and cannot be evaluated or translated to
SQL. See the [compiler guide](../guides/compiling.md) for the public flow.

## Syntax limits and cancellation

`syntax.ParseQuery` accepts a context and parser configuration. Zero limit fields
select the finite defaults; `hff.Limits.MaxQueryBytes` limits UTF-8 source bytes
and `MaxSyntaxDepth` limits meaningful expression nesting. `MaxCallbackDepth`
limits nested callback structure during compilation, with an outermost callback
at depth 1 and a default of 64. The current defaults for query bytes and syntax
depth are 65,536 bytes and 64 levels. The root expression is depth zero.

Depth counts parentheses, operators, collection nesting, guards, access parts,
and callback bodies. Singleton logical wrappers and access nodes with no suffix
do not add depth. The parser checks query size before parsing and checks the
resulting typed tree afterward; a preflight scan bounds delimiter, guard, and
unary-operator runs before entering the generated parser. The generated parser
also retains an internal rule-call safety limit.

The preflight scanner checks cancellation at byte-position checkpoints roughly
every 1 KiB. Escaped characters inside quoted strings can advance past a
checkpoint, so the scanner tracks the next checkpoint instead of relying on
an exact modulo boundary. This keeps long escaped strings and quoted names
cancellable without modifying generated parser code.

Query UTF-8 admission uses the shared chunked validator, checking context
between chunks of at most 4,096 bytes before parsing. If the source is invalid,
the diagnostic-location rescan checks context at approximately 1 KiB intervals
while preserving its byte spans. Cancellation is also checked before parsing,
periodically during the preflight scan, after generated parsing, and while
validating the resulting tree. The generated `ParseAST` call has no cooperative
context hook. HFF detects cancellation that occurs during that call only after
parsing returns; it cannot preempt the generated parser mid-call. The byte and
depth limits bound input size and nesting, but do not promise an immediate
cancellation response within `ParseAST`.

Syntax diagnostics report a UTF-8 byte span and a one-based line and Unicode
code-point column. Invalid UTF-8, malformed strings (including unpaired
surrogate escapes), and malformed syntax are rejected. A completed syntax parse
only confirms that the source follows the grammar.

## Recovery and aggregated diagnostics

The grammar uses PEGO's `#error` labels for expected tokens and values, and
`#recover` only at a whole compact-predicate boundary. This lets the parser
report independent syntax mistakes, such as an unclosed callback, without
turning its recovered AST into an executable tree. For example,
`items:any(item=>item.price>100 item.active` reports the missing `)` at the
recovery boundary. The result remains an error; only a complete valid parse
returns a syntax tree.

Common compilation also collects independent semantic errors from separate
AND/OR operands, list/object entries, and method arguments. It stops along a
dependent expression chain after its first failure to avoid cascades. Errors
are ordered by source position and retain byte spans and line/column locations.
Cancellation or a resource limit ends the operation immediately rather than
returning a partial diagnostic group. See the [compiler guide](../guides/compiling.md)
for the public `hff.Diagnostics` handling contract.

Run focused recovery and aggregation tests with:

```sh
go test ./internal/syntax ./engine -run 'TestRecoveryReportsIndependentSyntaxErrors|TestCompileAggregatesIndependentFailures|TestCompileDiagnosticsDoNotHideBudgetAbort'
```
