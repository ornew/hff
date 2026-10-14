# Serialized HFF configuration

HFF configuration has two parts: an engine schema and language policy, then a
backend mapping from HFF paths to physical storage. A versioned JSON document
lets an application or browser tool transport those settings as data. The
host validates the document, compiles HFF input, and asks the selected backend
to build its artifact. PostgreSQL Build returns a `WHERE` expression and
ordered parameters; it does not execute SQL or connect to a database.

The serialized format is strict. A document identifies its format version,
contains separate `engine` and `backend` objects, and uses explicit tags for
schema types, bindings, and SQL predicates. Duplicate object keys, malformed
or unpaired Unicode escapes, unknown versions, fields, binding tags, or
registry IDs are errors. This prevents a typo or unsupported storage shape
from silently changing the configuration.

## Describe the engine schema

Schema descriptors are recursive. A root struct names its fields, and each
field has a type and an independent optional marker. Include `null` in the
field type when a present value may be null. Optional permits an absent key;
nullable permits a present null value. Input validation also accepts an absent
nullable field when `optional` is false, so the two schema properties remain
distinct even though both can allow absence.

For example, this fragment describes an optional `nickname` that may also be
null, an ordered list of Int values, and a map with String values:

```json
{
  "kind": "struct",
  "fields": [
    {"name": "id", "type": {"kind": "int"}},
    {
      "name": "nickname",
      "type": {"kind": "union", "members": [
        {"kind": "null"}, {"kind": "string"}
      ]},
      "optional": true
    },
    {"name": "scores", "type": {
      "kind": "list", "element": {"kind": "int"}
    }},
    {"name": "labels", "type": {
      "kind": "map", "element": {"kind": "string"}
    }}
  ]
}
```

Capability IDs and resource limits belong to the engine configuration. The
document records the explicit capability selection and configured limits,
including whether limits are defaulted or explicitly unlimited. Extension
declarations record signatures, not implementations.

## Map schema paths to PostgreSQL storage

The backend object identifies PostgreSQL and contains a list of tagged
bindings. An HFF path is an array of literal schema field names. A SQL column
reference is an array of separately quoted identifier components. For example,
`["orders", "customer_id"]` means two SQL identifiers, while `["customer.id"]`
is one identifier containing a dot.

```json
{
  "version": 1,
  "engine": {
    "schema": {"kind": "struct", "fields": [
      {"name": "id", "type": {"kind": "int"}},
      {"name": "created", "type": {"kind": "timestamp"}},
      {"name": "profile", "type": {"kind": "map", "element": {"kind": "any"}}}
    ]},
    "capabilities": ["access:field", "operator:comparison"],
    "limits": {},
    "unlimitedLimits": false,
    "methods": []
  },
  "backend": {
    "kind": "postgresql",
    "bindings": [
      {"kind": "native", "path": ["id"], "column": ["orders", "id"]},
      {"kind": "timestamp", "path": ["created"],
       "column": ["orders", "created_at"],
       "remainder": ["orders", "created_ns"]},
      {"kind": "json", "path": ["profile"],
       "column": ["orders", "profile"], "format": "jsonb",
       "metadata": ["orders", "profile_type_map"]}
    ],
    "executionLimits": {},
    "translatorIDs": []
  }
}
```

The sample has a native Int column, a precision-preserving Timestamp pair, and
a JSONB map whose numeric types may require a metadata column. For Timestamp,
the `timestamptz` value stores whole microseconds and the optional smallint
remainder stores 0–999 nanoseconds. The caller guarantees both columns are
simultaneously null or non-null and that the remainder is in range. Without a
remainder column, the caller guarantees every stored Timestamp is
microsecond-exact. HFF year 0000 maps to PostgreSQL 1 BC.

Other binding variants represent a native PostgreSQL composite, a flattened
struct, and a correlated relation. A composite names its schema path and
physical composite column. A flattened struct carries explicit `present` and
`null` predicate trees with relative child bindings. A relation carries its
table and alias, typed correlation, optional list-state predicates, item type
and item binding, and optional unique order columns. Decode validates binding
descriptors against the engine schema where their binding kind defines a
schema relationship. It does not inspect database catalogs or stored rows;
PostgreSQL Build remains authoritative about whether a particular program
operation is supported by the selected bindings.

The following fragments show the remaining binding tags. A composite maps one
unambiguous struct value to one native composite column:

```json
{"kind":"composite","path":["address"],"column":["orders","address"]}
```

A flattened struct maps its fields separately. Its state expressions are
explicit typed predicates or IDs for expressions installed in the host's
trusted registry; children use paths relative to the parent:

```json
{
  "kind":"struct",
  "path":["contact"],
  "present":{"kind":"trusted","id":"contact.present.v1"},
  "null":{"kind":"trusted","id":"contact.null.v1"},
  "fields":[
    {"kind":"native","path":["email"],"column":["orders","contact_email"]},
    {"kind":"native","path":["phone"],"column":["orders","contact_phone"]}
  ]
}
```

A relation binding describes a caller-owned row source and typed correlation.
Its item binding has an empty relative path, and an order tuple is included
only when the HFF operation observes row order:

```json
{
  "kind":"relation",
  "path":["items"],
  "relation":{
    "table":["order_items"],
    "alias":"item",
    "correlation":{"kind":"equal",
      "left":{"kind":"column","column":["item","order_id"],"type":{"kind":"int"}},
      "right":{"kind":"column","column":["orders","id"],"type":{"kind":"int"}}},
    "present":{"kind":"bool","value":true},
    "null":{"kind":"bool","value":false},
    "itemType":{"kind":"struct","fields":[
      {"name":"price","type":{"kind":"int"}},
      {"name":"active","type":{"kind":"bool"}}
    ]},
    "item":{"kind":"struct","path":[],
      "present":{"kind":"bool","value":true},
      "null":{"kind":"bool","value":false},
      "fields":[
        {"kind":"native","path":["price"],"column":["item","price"]},
        {"kind":"native","path":["active"],"column":["item","active"]}
      ]},
    "order":[{"column":["item","id"],"type":{"kind":"int"}}]
  }
}
```

## Use typed predicates and registered trusted behavior

Relation and flattened-struct state use a tagged predicate tree. Its tags are
`trusted`, `bool`, `equal`, `null`, `notnull`, `value`, `and`, `or`, and `not`.
Scalar nodes use `column` with a `column` identifier array and HFF `type`, or
`literal` with a typed `value`. A literal stores its kind and canonical text.
The backend quotes identifiers and binds literal values as parameters.

If a state expression needs raw developer-authored SQL, the document names a
stable trusted-predicate ID. The SQL text and referenced columns live in the
host's registry, not in JSON. Extension declarations are also data, but their
PostgreSQL translators are registered executable code and are selected by
stable IDs. Decoding fails when an ID is missing. A document cannot inject SQL
or supply callback source.

The registry is part of the application's trusted configuration. Only
application-installed IDs can resolve; a document cannot create a new
trusted entry. `TrustedSQL` must remain deterministic and side-effect-free,
and its host entry must be reviewed like code.

## Validate, then build

Configuration decoding validates document syntax, schema types, binding
variants and schema relationships, identifiers, relation predicates, and
registered IDs before returning typed configuration. PostgreSQL Build then
validates whether the compiled program can be represented by those bindings.
It returns a predicate and ordered arguments but performs no database I/O.
The caller integrates those values with its statement and driver; the
Playground can show them without connecting to a database.

`Decode` limits one document to 4 MiB and nested JSON arrays and objects to
depth 512, counting the root object as depth one. `Encode` applies the same
document-size limit to its output.

The version-one format preserves configuration semantics, not the history of
Go option calls. It does not serialize compiled programs, database connections,
or function values. See the [design record](../design/022-versioned-configuration-format.md)
for the complete descriptor and trust-boundary contract.

## Encode and decode in Go

The `config` package exposes `Encode` and `Decode`. Both use a host-owned
registry for trusted SQL predicates and method translators:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/config"
	"github.com/ornew/hff/engine"
)

func main() {
	schema := hff.NewStructType(
		hff.Field{Name: "age", Type: hff.PrimitiveType(hff.IntType)},
	)
	engineConfig := engine.Config{
		Schema:       schema,
		Capabilities: capabilities.Standard(),
	}
	sqlConfig := sql.Config{
		Dialect: sql.PostgreSQL,
		Bindings: []sql.Binding{
			sql.Bind(hff.Path{"age"}, sql.Column("people", "age")),
		},
	}

	registry := config.Registry{}
	data, err := config.Encode(engineConfig, sqlConfig, registry)
	if err != nil {
		log.Fatal(err)
	}
	loaded, err := config.Decode(data, registry)
	if err != nil {
		log.Fatal(err)
	}

	e, err := engine.New(engine.WithConfig(&loaded.Engine))
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(context.Background(), `age>18`)
	if err != nil {
		log.Fatal(err)
	}
	query, err := sql.Build(context.Background(), program, sql.WithConfig(&loaded.SQL))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("WHERE: %s\nArgs: %#v\n", query.Where(), query.Args())
}
```

`Encode` validates that its configurations can be restored with the supplied
registry. `Decode` returns reusable engine and SQL configurations; the caller
passes `loaded.SQL` to PostgreSQL `sql.Build` for a particular compiled
program. The decoder does not connect to a database or execute registered
code.
