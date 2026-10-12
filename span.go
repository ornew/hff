package hff

import "github.com/ornew/hff/internal/core"

// Span is a half-open range [Start, End) of UTF-8 byte offsets in the original
// query, not rune indexes. Source diagnostics report one-based lines and
// columns counted in Unicode code points. A zero Span is an empty range at byte
// offset zero; use the containing query to interpret it.
type Span = core.Span
