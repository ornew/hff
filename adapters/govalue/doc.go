// Package govalue adapts Go records to HFF while keeping reflection out of the
// compiler and backend contracts.
//
// Choose NewBinding[T] when the Go type is the input contract. It infers the HFF
// schema once and provides Binding.Build and Filter.Match for typed Go records.
// Struct names prefer hff tags, then json tags, then exported Go field names;
// omitempty permits missing fields. Recursive Go types and ambiguous promoted
// field names are rejected during construction.
//
// Binding object roots are nonnullable: a pointer to a struct or a string-keyed
// map can configure an engine, but a nil root record is rejected as invalid
// input. Nested nil pointers, maps, and slices become explicit HFF nulls. Other
// root types can be inferred for conversion-only use without an engine.
//
// Choose New with WithSchema and WithStructs when an externally defined HFF
// schema should stay independent of Go structs. Explicit StructMapping maps HFF
// keys to direct exported Go field names; tags and embedded promotion are not
// inferred by this converter. String-keyed maps need no struct mapping.
//
// Conversion preserves numeric kinds and distinguishes missing keys from null.
// It creates owned immutable HFF values, validates the resulting schema, and
// returns typed errors without partial output. Keep source records unchanged
// during conversion or matching; a shared Binding or Converter may otherwise
// be used concurrently. There is no conversion back into mutable Go records.
package govalue
