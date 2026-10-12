# Flattened PostgreSQL Struct Bindings

Use `BindStruct` when an HFF struct is stored across separate SQL columns and
the database representation does not itself encode whether the parent struct
is absent, null, or present. The caller supplies explicit, trusted SQL Boolean
expressions for parent presence and null state.

For example, this table spreads the HFF `profile` object across two nullable
columns and stores its state separately:

```sql
CREATE TABLE records (
    id bigint PRIMARY KEY,
    profile_present boolean NOT NULL,
    profile_null boolean NOT NULL,
    profile_name text,
    profile_age bigint,
    CHECK (profile_present OR NOT profile_null)
);
INSERT INTO records VALUES
    (1, false, false, NULL, NULL), -- missing profile
    (2, true,  true,  NULL, NULL), -- profile is null
    (3, true,  false, NULL, NULL), -- present object; both children null
    (4, true,  false, 'Ada', 42);  -- present object with values
```

The HFF schema for this physical shape must allow the parent to be both
optional and nullable, and allow the child nulls shown in row 3:

```go
profileType := schema.Struct(
    schema.Nullable(schema.String("name")),
    schema.Nullable(schema.Int("age")),
)
recordType := schema.Struct(
    schema.Optional(schema.Nullable(schema.Field("profile", profileType))),
)
```

The reconstructed field is absent in row 1, explicit null in row 2, and an
object containing two explicit null values in row 3. The two Boolean state
columns make these cases distinguishable; checking whether both child columns
are SQL null would collapse them.

## Bind parent state and fields

```go
profile := hffsql.BindStruct(
    hff.Path{"profile"},
    hffsql.AsPredicate(hffsql.ColumnValue(
        hffsql.Column("records", "profile_present"), schema.BoolType,
    )),
    hffsql.AsPredicate(hffsql.ColumnValue(
        hffsql.Column("records", "profile_null"), schema.BoolType,
    )),
	hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "profile_name")),
	hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "profile_age")),
)

query, err := hffsql.Build(ctx, program,
	hffsql.WithDialect(hffsql.PostgreSQL),
	hffsql.WithBindings(profile),
)
```

The first path identifies the HFF struct. Child binding paths contain one
field name relative to that struct. A nested `BindStruct` supplies independent
presence and null expressions for a child struct. A nested `Bind`, `BindJSON`,
`BindComposite`, or `BindRelation` can bind a supported field representation.
Required schema fields need bindings; an unbound optional field is absent from
the HFF object.

The example uses `AsPredicate(ColumnValue(..., schema.BoolType))` because each
state is already stored in a Boolean column. For a state derived from another
column, combine typed predicates such as `Equal`, `IsNull`, `And`, and `Or`.
`TrustedSQL` is available for backend-native expressions the typed constructors
cannot express; it is raw service-authored SQL, not parsed or validated by
HFF. Any expression used for parent state must be deterministic and
side-effect-free; HFF cannot prove this contract. Its `ColumnRef` arguments
only support alias bookkeeping. See the
[relation guide](relations.md#build-correlation-predicates-safely) for the
security and portability limits. Query literals remain bound parameters.

## Preserve HFF states

The `present` expression is true only when the struct key exists. The `null`
expression is true only when that present HFF value is logically null. For this
spread-column representation, that logical state comes from the explicit flag;
there is no single parent SQL column whose nullness can be inferred. An absent
optional parent becomes a missing HFF field; a present null parent becomes HFF
null. A present object whose child columns are all null stays an object with
explicit null children. Do not derive parent state by checking whether all
child columns are null. A scalar SQL `NULL` is an explicit null value, never a
missing field.
In the table above, bind these states from `profile_present` and
`profile_null` as shown in the code; bind `name` and `age` as relative child
fields to their respective columns.

Nested flattened parents need their own state expressions. Guarded field access
uses those state channels to retain missing, null, and no-value behavior.
`Build` does not connect to PostgreSQL or inspect rows; the service owns the
schema and must store values consistent with the HFF declaration.

For `BindStruct` combined with a PostgreSQL relation, see the
[relation binding guide](relations.md). Its runnable example shows a
struct-bound relation item; the flattened-parent mapping above is a separate
pattern using explicit parent-state expressions and relative child bindings:

```sh
go run ./examples/sql-relations
```

## Limits

Flattened struct fields can be projected individually, but parent presence is
always explicit. A child path guarded by a nullable or optional parent cannot
be represented by an ordinary scalar `Bind` alone. If the database already
stores a native composite, prefer [`BindComposite`](composites.md), which
preserves SQL-null row identity separately from null attributes.
