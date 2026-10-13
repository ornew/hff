# Filter Many-to-Many Memberships Through a View

`BindRelation` takes one table or view as its row source. To expose a
many-to-many relationship, make the join explicit in a PostgreSQL view and
bind the view rows as an HFF list.

## Tables, view, and sample rows

```sql
CREATE TABLE users (id bigint PRIMARY KEY, name text NOT NULL);
CREATE TABLE groups (id bigint PRIMARY KEY, slug text NOT NULL UNIQUE);
CREATE TABLE memberships (
    user_id bigint NOT NULL REFERENCES users(id),
    group_id bigint NOT NULL REFERENCES groups(id),
    PRIMARY KEY (user_id, group_id)
);
CREATE VIEW user_group_rows AS
SELECT m.user_id, g.id AS group_id, g.slug
FROM memberships AS m
JOIN groups AS g ON g.id = m.group_id;

INSERT INTO users VALUES (1, 'Ada'), (2, 'Lin'), (3, 'Sam');
INSERT INTO groups VALUES (10, 'platform'), (20, 'security');
INSERT INTO memberships VALUES (1, 10), (2, 10), (2, 20), (3, 20);
```

The logical query shape is `users.groups[]`, with each item containing the
group ID and slug. Users 1 and 2 belong to `platform`; user 3 does not.
For example, user 2 is reconstructed as:

```json
{"groups":[{"id":10,"slug":"platform"},{"id":20,"slug":"security"}]}
```

## Bind the view rows

```go
func platformUsers(ctx context.Context, db *sql.DB) ([]int64, error) {
	groupType := schema.Struct(schema.Int("id"), schema.String("slug"))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.List("groups", groupType))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		return nil, err
	}
	program, err := e.Compile(ctx, `groups:any(group=>group.slug="platform")`)
	if err != nil {
		return nil, err
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("user_group_rows"), "group_row"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("group_row", "user_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("users", "id"), schema.IntType),
		)),
		hffsql.WithRelationItem(groupType,
			hffsql.BindStruct(hff.Path{}, hffsql.Boolean(true), hffsql.Boolean(false),
				hffsql.Bind(hff.Path{"id"}, hffsql.Column("group_row", "group_id")),
				hffsql.Bind(hff.Path{"slug"}, hffsql.Column("group_row", "slug")),
			),
		),
	)
	if err != nil {
		return nil, err
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindRelation(hff.Path{"groups"}, relation)),
	)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT users.id FROM users WHERE `+query.Where(), query.Args()...)
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

The function imports `context`, `database/sql`, and the HFF `hff`, `hffsql`,
`capabilities`, `engine`, and `schema` packages. The service executes the
complete parent query and passes `query.Args()` separately. For the sample
rows, the result is `[]int64{1, 2}`.

The view is the complete relation source; HFF does not generate its joins or
discover membership tables. Add a typed unique order only if the HFF query
observes the list order, such as by indexing or returning the collection.
See the
[relation shape guide](../backends/postgresql/relations.md#which-shapes-fit-a-relation)
for view and nested-relation alternatives.
