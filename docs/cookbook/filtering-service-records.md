# Filter Service Records

This recipe combines common checks for a service record. Assume the schema has
`status` as a string, `owner` as an optional object with a nullable string
`email`, `labels` as a string-keyed map, and `items` as a list of objects with
numeric `price` and `quantity` fields.

## Match a useful record

```hff
status:in("open", "pending")
owner!.email~"@example\\.com$"
labels.has("tenant") labels.get("tenant")="north"
items:any(item=>item.price*item.quantity>=100)
```

The first predicate admits only active statuses. The postfix guard makes the
optional owner path safe; when the owner is absent or has the wrong shape, that
predicate is false. The two metadata checks distinguish a missing key from a
stored null. The collection callback selects records with at least one
qualifying line item.

## Relax a condition safely

When a query accepts dynamic values, narrow the receiver before calling a
family-specific method:

```hff
metadata.get("region"):[string]:lower="north"
```

The type guard preserves a matching string. A value of another type becomes
internal no-value, which converts to a false predicate. If a missing value
should also be accepted, express that policy explicitly with a separate
predicate and ordinary short-circuit operators.

## Keep collection and null behavior explicit

`has()` checks presence even when a value is null. `get()` returns null for
both a missing key and a present null value, so combine them when presence
matters. List callbacks short-circuit left-to-right, and cancellation or a
resource-limit error remains an error rather than a non-match.

See the [collection guide](../guides/collections.md),
[callback guide](../guides/callbacks.md), and
[evaluation specification](../../spec/evaluation.md) for the complete rules.
