package core

// Span is a half-open range of UTF-8 byte offsets in the original query.
type Span struct{ Start, End int }
