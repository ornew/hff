# Compare Expected and Actual JSON

Use dynamic comparison when a document stores an expected value beside an
observed value and their types are known only at runtime. This example keeps
rows whose values are equal, including recursively equal arrays and objects.

Assume each row has a JSONB document and a version-1 numeric type sidecar:

```sql
CREATE TABLE checks (
    id bigint PRIMARY KEY,
    event jsonb NOT NULL,
    event_types jsonb NOT NULL
);

INSERT INTO checks VALUES
  (101,
   '{"expected":{"count":9007199254740993,"labels":["a",null]},"actual":{"count":9007199254740993,"labels":["a",null]}}',
   '{"version":1,"types":{"/expected/count":{"type":"int"},"/actual/count":{"type":"int"}}}'),
  (102,
   '{"expected":{"steps":[1,2]},"actual":{"steps":[2,1]}}',
   '{"version":1,"types":{"/expected/steps/0":{"type":"int"},"/expected/steps/1":{"type":"int"},"/actual/steps/0":{"type":"int"},"/actual/steps/1":{"type":"int"}}}'),
  (103,
   '{"expected":42,"actual":"42"}',
   '{"version":1,"types":{"/expected":{"type":"int"}}}'),
  (104,
   '{"expected":1,"actual":1.0}',
   '{"version":1,"types":{"/expected":{"type":"int"},"/actual":{"type":"float"}}}');
```

Declare both document members as `Any`, compile the query, and bind the
complete `event` value once. In the application function below, `ctx` and `db`
are its `context.Context` and `*sql.DB` parameters:

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

func filterChecks(ctx context.Context, db *sql.DB) ([]int64, error) {
	eventType := schema.Struct(
		schema.Any("expected"),
		schema.Any("actual"),
	)
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Field("event", eventType))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		return nil, err
	}

	program, err := e.Compile(ctx, `event.expected=event.actual`)
	if err != nil {
		return nil, err
	}

	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(
			hff.Path{"event"},
			hffsql.Column("checks", "event"),
			hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("checks", "event_types")),
		)),
	)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT checks.id FROM checks WHERE `+query.Where(), query.Args()...)
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

The runnable [translator example](https://github.com/ornew/hff/tree/main/examples/sql-json-comparison)
builds the same predicate without opening a database:

```sh
go run ./examples/sql-json-comparison
```

Executing the predicate against the sample table selects IDs `101` and `104`.
ID `101` verifies exact int64 equality above the float64 exact-integer range,
with nulls compared at the same nested positions.
ID `104` shows that integer `1` and float `1.0` compare equal by numeric value
when the sidecar preserves their HFF types. ID `102` differs because list
order matters. ID `103` compares incompatible top-level families, producing
ordinary HFF null and therefore no matching row.

The caller must store sidecar entries consistently with the JSONB document and
declared schema. `Build` does not inspect or validate stored metadata. Native
`json` preserves numeric spelling and does not need the sidecar for those
distinctions. See the [JSON binding guide](../backends/postgresql/json.md) for
numeric recovery, null/no-value behavior, and the supported comparison scope.
