// Package schema builds immutable HFF input type descriptors with field helpers.
//
// Helpers such as String("name") and Int("age") return named fields, not types.
// Struct combines those fields into an object type. StringType and IntType are
// type descriptors for list elements, map values, union members, or extension
// signatures. For example, List("tags", StringType) defines a named list field,
// while ListOf(StringType) describes a list wherever a type is required.
//
// Optional and Nullable model different input states. Optional(String("name"))
// permits the key to be missing; Nullable(String("name")) permits a present null
// value. Input validation also accepts absence for a nullable field, while
// keeping an absent key distinct from explicit null. Optional does not itself
// permit null. The wrappers retain separate schema flags and can be composed;
// neither changes how a present, non-null string is validated.
//
// A Struct has declared keys and per-field types. A MapOf(StringType) accepts
// arbitrary string keys whose values are strings. Union(IntType, StringType)
// accepts either representation without converting between them. AnyType
// accepts all valid HFF values and gives the compiler less static information;
// explicit type guards can narrow dynamic values in queries.
//
// Constructors assemble descriptors without returning errors. Call Type.Check
// to validate a descriptor on its own, or pass it to engine.New, which validates
// the complete input schema and requires a struct or map root. Descriptors and
// their field/member snapshots are immutable and safe to reuse across engines.
package schema
