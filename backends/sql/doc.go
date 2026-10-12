// Package sql translates compiled HFF Programs into parameterized PostgreSQL
// predicates. It does not open a database connection or execute SQL.
//
// Compile a query with an engine, then call Build with an explicit dialect and
// bindings for the HFF fields that query uses. Bindings describe storage, not
// query syntax: Bind maps a scalar column, BindJSON maps a document,
// BindComposite maps a native struct, BindStruct maps fields spread across
// columns, and BindRelation maps a list to correlated rows.
//
// Query.Where returns a predicate fragment, not a SELECT statement. Append it
// to a caller-owned statement and pass Query.Args() to the database driver as
// parameters. Column quotes literal identifier components; it does not accept
// SQL expressions. HFF paths and physical SQL names are independent.
//
// Use ColumnValue, Literal, Equal, IsNull, And, and related constructors for
// parent-state and relation predicates. Their scalar types describe a contract
// with the application's database schema; Build does not inspect actual column
// types. TrustedSQL accepts reviewed raw source as an escape hatch and does not
// parse, sanitize, or prove its meaning, purity, or portability.
//
// Build rejects operations whose HFF meaning cannot be preserved by the
// selected representation. JSONB numeric metadata can restore supported numeric
// distinctions; its consistency with data is a caller-owned storage contract.
// Profile and Query.Profile expose support and guarantees separately from the
// engine's capability policy. PostgreSQL execution does not enforce HFF step
// budgets; the application configures database timeouts and resource controls.
package sql
