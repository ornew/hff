# Filter JSONB Numeric Thresholds

When JSONB normalizes a number's HFF subtype, store a versioned JSON Pointer
sidecar with the document. This example compares two dynamic prices and keeps
rows where the actual price is greater than the expected price.

## Document and metadata rows

```sql
CREATE TABLE quotes (
    id bigint PRIMARY KEY,
    event jsonb NOT NULL,
    event_types jsonb NOT NULL
);

INSERT INTO quotes VALUES
    (201,
     '{"expected":{"amount":125.75},"actual":{"amount":130.25}}',
     '{"version":1,"types":{"/expected/amount":{"type":"float"},'
       '"/actual/amount":{"type":"float"}}}'),
    (202,
     '{"expected":{"amount":250.0},"actual":{"amount":120.0}}',
     '{"version":1,"types":{"/expected/amount":{"type":"float"},'
       '"/actual/amount":{"type":"float"}}}'),
    (203,
     '{"expected":{"amount":100.0},"actual":{"amount":"130"}}',
     '{"version":1,"types":{"/expected/amount":{"type":"float"}}}');
```

The schema exposes `event.expected.amount` and `event.actual.amount` as
`Any`. The sidecar restores Float for the two numeric fields in rows 201 and
202. Row 203 has a string in `actual.amount`, so its comparison is a family
mismatch and does not match the filter.

## Compile, bind, and execute

```go
func increasingQuotes(ctx context.Context, db *sql.DB) ([]int64, error) {
	amounts := schema.Struct(schema.Any("amount"))
	eventType := schema.Struct(
		schema.Field("expected", amounts),
		schema.Field("actual", amounts),
	)
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Field("event", eventType))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		return nil, err
	}
	program, err := e.Compile(ctx, `event.actual.amount>event.expected.amount`)
	if err != nil {
		return nil, err
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(
			hff.Path{"event"}, hffsql.Column("quotes", "event"), hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("quotes", "event_types")),
		)),
	)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT quotes.id FROM quotes WHERE `+query.Where(), query.Args()...)
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

This function is a complete recipe body; imports are `context`, `database/sql`,
and the HFF `hff`, `hffsql`, `capabilities`, `engine`, and `schema` packages.
The application supplies the database connection. Executing the statement
returns only ID `201`.

Each metadata pointer is relative to the bound JSON document. The caller is
responsible for keeping the version-1 sidecar consistent with the JSONB value;
`Build` does not query or validate stored rows. For pointer escaping, nested
lists, representation limits, and the broader operation matrix, see the
[PostgreSQL JSON guide](../backends/postgresql/json.md#storage-requirements-and-limits).
