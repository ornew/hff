# Search Dynamic JSON Labels

Search services whose `team` label is either a string or a list of strings.
The same filter handles both shapes and returns matching service IDs.

```sql
CREATE TABLE services (
    id bigint PRIMARY KEY,
    labels jsonb NOT NULL
);

INSERT INTO services VALUES
    (1, '{"team":"platform","environment":"production"}'),
    (2, '{"team":["platform","on-call"],"environment":"production"}'),
    (3, '{"team":"internal","environment":"production"}'),
    (4, '{"team":["platform"],"environment":"staging"}');
```

Declare the union in the schema, then bind the JSONB column at the root HFF
path `labels`. `Build` translates the predicate; the caller executes it with
the returned SQL fragment and bound arguments:

```go
package main

import (
	"context"
	"database/sql"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func filterServices(ctx context.Context, db *sql.DB) ([]int64, error) {
	teamType := schema.Union(
		schema.StringType,
		schema.ListOf(schema.StringType),
	)
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Map("labels", teamType))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		return nil, err
	}

	program, err := e.Compile(ctx,
		`labels.team:contains("platform") labels.environment="production"`)
	if err != nil {
		return nil, err
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(
			hff.Path{"labels"},
			hffsql.Column("services", "labels"),
			hffsql.JSONB,
		)),
	)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT services.id FROM services WHERE `+query.Where(), query.Args()...)
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

Executing this predicate against the sample table selects IDs `1` and `2`.
The string form uses substring matching; the list form searches for an equal
string element. ID `3` has another team, and ID `4` has a non-production
environment. Because this schema allows only strings and lists of strings,
this filter does not need numeric JSONB metadata. The
[runnable translator example](https://github.com/ornew/hff/tree/main/examples/sql-label-search)
builds the same filter without opening a database:

```sh
go run ./examples/sql-label-search
```

Arguments are evaluated left to right. A failed containment check does not
skip later arguments. Null string receivers or reached null substring
arguments propagate null; a list `contains()` can search for a stored null
only when the declared list element type permits null (for example, a nullable
element or `Any`). See the [PostgreSQL JSON guide](../backends/postgresql/json.md)
for dynamic dispatch, `in()` and `unique()` support, and null/no-value rules.
