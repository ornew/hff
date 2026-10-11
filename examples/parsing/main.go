// Command parsing demonstrates syntax-only parsing from inside the HFF module.
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
		`age > 10`,
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
