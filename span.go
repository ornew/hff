package hff

import "github.com/ornew/hff/internal/core"

// Span is a half-open range of UTF-8 byte offsets in the original query.
// Errors that include source locations use one-based lines and columns counted
// in Unicode code points.
type Span = core.Span
