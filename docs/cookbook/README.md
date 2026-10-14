# Introduction

These short recipes show how to express common service filters. Each recipe
states the data shape it expects and the query to use; it complements the
step-by-step [tutorials](../tutorials/README.md) and semantic
[guides](../guides/README.md).

- [Filter service records](filtering-service-records.md): combine status,
  optional nested values, metadata, and collection predicates.
- [Compare expected and actual JSON](compare-dynamic-json-values.md): compare
  dynamic JSON fields, including nested structures and exact large integers.
- [Search dynamic JSON labels](search-dynamic-json-labels.md): search a label
  stored as either one string or a list of strings in plain JSONB.
- [Filter flattened nullable profiles](postgresql-flattened-profile.md): map
  scalar columns and explicit missing/null parent state to one HFF record.
- [Filter nested order relations](postgresql-nested-relations.md): correlate
  line items and their adjustments without losing which rows belong together.
- [Filter JSONB numeric thresholds](postgresql-jsonb-numeric-thresholds.md):
  compare dynamically typed numeric fields with caller-maintained type metadata.
- [Use tagged JSON union values](postgresql-tagged-json-unions.md): run a
  Duration/list JSONB example and learn when tagged scalar branches can be
  recovered alongside containers and scalar alternatives.
- [Select tagged values from JSON object entries](postgresql-json-items.md):
  preserve key and value types while filtering `items()` pairs from a JSONB map.
- [Filter many-to-many memberships](postgresql-many-to-many-view.md): expose
  a join through one PostgreSQL view and bind the view rows as an HFF list.

For exact operator and method contracts, see the [reference](../reference/README.md).
