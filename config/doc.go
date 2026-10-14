// Package config encodes and decodes versioned HFF engine and PostgreSQL
// binding configurations as JSON. A document contains schema, capability,
// limit, and binding data; executable SQL translators and developer-authored
// trusted predicates remain in a host-owned Registry and are referenced by ID.
//
// Encode validates and serializes existing configurations. Decode strictly
// validates one document and returns configurations for engine.New and
// sql.Build. Neither function compiles a filter or contacts a database.
//
// A typical service decodes a saved configuration once at startup, constructs
// an Engine from Loaded.Engine, compiles each filter, and builds a SQL WHERE
// expression using Loaded.SQL. See the serialized configuration guide for a
// complete example and the versioned configuration design record for the wire
// format.
package config
