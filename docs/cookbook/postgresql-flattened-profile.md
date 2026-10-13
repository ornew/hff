# Filter a Flattened Nullable Profile

Use `BindStruct` when a logical object is spread across columns and the table
stores its parent state separately. This example distinguishes a missing
profile, a null profile, a present profile with a null email, and a populated
profile.

## Data shape

```sql
CREATE TABLE accounts (
    id bigint PRIMARY KEY,
    status text NOT NULL,
    profile_present boolean NOT NULL,
    profile_null boolean NOT NULL,
    profile_email text,
    CHECK (profile_present OR NOT profile_null),
    CHECK (profile_present OR profile_email IS NULL),
    CHECK (NOT profile_null OR profile_email IS NULL)
);

INSERT INTO accounts VALUES
    (1, 'active',   true,  false, 'ada@example.com'),
    (2, 'active',   false, false, NULL),
    (3, 'active',   true,  true,  NULL),
    (4, 'active',   true,  false, NULL),
    (5, 'inactive', true,  false, 'lin@example.com');
```

The logical HFF record for each row is:

```text
1  {"status":"active","profile":{"email":"ada@example.com"}}
2  {"status":"active"}                         // profile is missing
3  {"status":"active","profile":null}
4  {"status":"active","profile":{"email":null}}
5  {"status":"inactive","profile":{"email":"lin@example.com"}}
```

The profile field is both optional and nullable. Its child email is nullable:

```go
profileType := schema.Struct(
	schema.Nullable(schema.String("email")),
)
recordType := schema.Struct(
	schema.String("status"),
	schema.Optional(schema.Nullable(schema.Field("profile", profileType))),
)
```

## Compile and bind

The guard on `profile` makes the email predicate false when the parent is
missing or null. A null email also does not match the regular expression.

```hff
status="active" profile!.email~"@example\\.com$"
```

The following function builds the predicate and runs it with caller-owned SQL:

```go
func matchingAccounts(ctx context.Context, db *sql.DB) ([]int64, error) {
	profileType := schema.Struct(schema.Nullable(schema.String("email")))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.String("status"),
			schema.Optional(schema.Nullable(schema.Field("profile", profileType))),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		return nil, err
	}
	program, err := e.Compile(ctx, `status="active" profile!.email~"@example\\.com$"`)
	if err != nil {
		return nil, err
	}
	profile := hffsql.BindStruct(
		hff.Path{"profile"},
		hffsql.AsPredicate(hffsql.ColumnValue(
			hffsql.Column("accounts", "profile_present"), schema.BoolType,
		)),
		hffsql.AsPredicate(hffsql.ColumnValue(
			hffsql.Column("accounts", "profile_null"), schema.BoolType,
		)),
		hffsql.Bind(hff.Path{"email"}, hffsql.Column("accounts", "profile_email")),
	)
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(
			hffsql.Bind(hff.Path{"status"}, hffsql.Column("accounts", "status")),
			profile,
		),
	)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT accounts.id FROM accounts WHERE `+query.Where(), query.Args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
```

Assume the function is in a file importing `context`, `database/sql`,
`github.com/ornew/hff`, `backends/sql` as `hffsql`, `capabilities`, `engine`,
and `schema`. The application supplies an opened database connection. Running
the statement against the rows above returns only ID `1`.

`profile_present` means the HFF key exists. `profile_null` means that present
parent is logically null. The child SQL `NULL` is an explicit null email; it
does not make the profile missing. HFF does not infer parent state from child
columns. See the [flattened binding guide](../backends/postgresql/flattened-structures.md)
for nested structs and parent-state contracts.
