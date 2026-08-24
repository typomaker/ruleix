# ruleix

`ruleix` is a strongly typed, in-memory rule index for Go. Given a concrete
value, it efficiently finds all stored rules whose constraints match that
value.

Use it when you have many rules with optional fields—for example pricing,
feature flags, promotions, routing, or audience targeting—and need to evaluate
the same schema repeatedly.

## The problem

Rule-based systems often need to answer the inverse of a typical lookup: given
one concrete value, find all stored rules whose optional constraints match it.

In SQL terms, a lookup with optional geographic constraints might resemble:

```sql
SELECT *
FROM rules
WHERE (store_id = $1 OR store_id IS NULL)
  AND (region_id = $2 OR region_id IS NULL)
  AND (district_id = $3 OR district_id IS NULL);
```

Here, `NULL` in a stored rule acts as a wildcard: the rule matches any concrete
value for that field. Queries with several such conditions can make efficient
use of conventional database indexes difficult.

A straightforward in-memory implementation scans every rule for every request
and checks each field individually. Its cost grows with both the number of rules
and the number of fields, especially when rules contain wildcards, ranges,
exclusions, or nested properties.

`ruleix` builds an immutable index from the rules once. Searches then combine
precomputed candidate sets for each constraint instead of evaluating every rule
from scratch.

## Features

- Generic, strongly typed API with no reflection.
- Equality, exclusion, ordered, interval, and dynamic comparison filters.
- Wildcards: a getter returning `ok == false` matches every concrete value.
- Multi-column rules composed with `All` and direct nested getters.
- Unique results in first-insertion order.
- Immutable indexes safe for concurrent searches after `Build`.
- Roaring bitmap-backed candidate sets with pooled search scratch space.
- Opt-in planner diagnostics without instrumentation on ordinary searches.

## Requirements

- Go 1.23 or newer.

## Installation

```sh
go get github.com/typomaker/ruleix
```

## Quick start

This example indexes rules across four different columns: country, customer
tier, minimum order total, and an excluded sales channel.

```go
package main

import (
	"cmp"
	"fmt"

	"github.com/typomaker/ruleix"
)

type Constraint struct {
	Country         *string
	Tier            *string
	MinimumTotal    *int
	ExcludedChannel *string
}

func pointer[T any](value T) *T { return &value }
func optional[T any](value *T) (T, bool) {
	if value == nil {
		var zero T
		return zero, false
	}
	return *value, true
}

func main() {
	schema := ruleix.All(
		ruleix.Include(func(c Constraint) (string, bool) { return optional(c.Country) }),
		ruleix.Include(func(c Constraint) (string, bool) { return optional(c.Tier) }),
		ruleix.GreaterOrEqual(func(c Constraint) (int, bool) { return optional(c.MinimumTotal) }, cmp.Compare[int]),
		ruleix.Exclude(func(c Constraint) (string, bool) { return optional(c.ExcludedChannel) }),
	)

	constraints := []Constraint{
		{
			Country:         pointer("DE"),
			Tier:            pointer("gold"),
			MinimumTotal:    pointer(100),
			ExcludedChannel: pointer("marketplace"),
		},
		{Country: pointer("DE")},
		{},
	}
	ids := []string{"gold-de", "all-de", "global"}

	entries := ruleix.Zip(constraints, ids)
	index, err := ruleix.New[Constraint, string](schema).Build(entries)
	if err != nil {
		panic(err)
	}

	query := Constraint{
		Country:         pointer("DE"),
		Tier:            pointer("gold"),
		MinimumTotal:    pointer(150),
		ExcludedChannel: pointer("web"),
	}
	var matches []string
	index.Search(query, &matches)

	fmt.Println(matches)
	// Output: [gold-de all-de global]
}
```

A runnable version is available in
[`examples/multiple_columns`](examples/multiple_columns).

There is also an example combining an interval with a strict `>` comparison in
[`examples/between_and_gt`](examples/between_and_gt). It uses `CompareBy`
to choose the comparison operator from each stored constraint; for a fixed
operator, use `Greater` or `Less` directly.

## Filters

| Filter | Stored constraint matches when |
| --- | --- |
| `Include` | query value equals the stored value |
| `Exclude` | query value does not equal the stored forbidden value |
| `GreaterOrEqual` | query value is greater than or equal to the stored value |
| `LessOrEqual` | query value is less than or equal to the stored value |
| `Greater` | query value is greater than the stored value |
| `Less` | query value is less than the stored value |
| `Between` | the query interval is fully covered by the stored interval |
| `CompareBy` | the operator stored with the constraint evaluates to true |
| `All` | every child rule matches |

Memory-sensitive scalar equality and ordered rules can opt into a bounded,
conservative representation. The exact representation is retained when it
fits; otherwise results may include false positives but never omit an exact
match:

```go
ruleix.Lossy(
	ruleix.Include(customerID),
	ruleix.MemoryLimit(20<<20),
)
```

One limit can cover an entire conjunction. The build keeps every child exact
when their combined accounted size fits; otherwise each child receives its
minimum viable representation before the remaining pooled bytes are divided
and redistributed among children that can improve:

```go
ruleix.Lossy(
	ruleix.All(ruleix.Include(customerID), ruleix.GreaterOrEqual(minimumTotal, cmp.Compare[int])),
	ruleix.MemoryLimit(20<<20),
)
```

All getters return `(value, ok)`. In a stored constraint, `ok == false` is a
wildcard. In a search value it only matches stored wildcards. For `Exclude`,
`ok == false` means that no value is forbidden. Zero values remain concrete
when returned with `ok == true`.

`Between` applies the following rule; either stored bound may be a wildcard:

```text
stored.from <= query.from AND query.until <= stored.until
```

`CompareBy` supports the typed `OperatorEQ`, `OperatorLT`, `OperatorLTE`,
`OperatorGT`, and `OperatorGTE` constants. Its value accessor is used for both
stored constraints and queries, while its operator accessor is used only when
building stored constraints. The operator present in a query is ignored. A
non-wildcard stored constraint must have a valid operator. A missing query
value matches only stored wildcards. Nested fields are read directly:

```go
ruleix.Include(func(c Constraint) (int, bool) {
	if c.Platform == nil || c.Platform.Version == nil {
		return 0, false
	}
	return c.Platform.Version.Major, true
})
```

## Building an index

`New` returns a reusable builder. `Build` accepts an `iter.Seq2` of constraints
and result IDs:

```go
index, err := ruleix.New[Constraint, string](schema).Build(
	func(yield func(Constraint, string) bool) {
		for _, row := range source {
			if !yield(row.Constraint, row.ID) {
				return
			}
		}
	},
)
```

`Zip` is a convenience for parallel slices and panics when their lengths differ.

For workloads that periodically rebuild the same schema, keep the builder and
call `Build` again. Every call creates independent mutable build state
and returns a new immutable index; indexes returned earlier remain safe for
concurrent searches. `Build` itself is not safe for concurrent calls on the
same builder: callers must provide synchronization when they need parallel
build coordination. A failed build does not prevent the next call:

```go
builder := ruleix.New[Constraint, string](schema)
first, err := builder.Build(firstEntries)
second, err := builder.Build(secondEntries)
```

## Searching

`Search` appends matches to a destination slice, allowing callers to accumulate
results across searches. Reset the slice explicitly when previous results are
not needed:

```go
var matches []string
if index.Search(value, &matches) {
	fmt.Println("matched", matches)
}

matches = matches[:0]
index.Search(anotherValue, &matches)
```

`Search` returns `true` when the current call finds at least one match. Elements
already present in the destination slice do not affect the return value.

The proposed design for a separate ranked-result layer and its `Search` and
`Match` signatures is recorded in [docs/rankix.md](docs/rankix.md).

When adjacent searches repeat constraint values, `Local` can reuse intermediate
bitmap results from `Include`, `Greater`, `GreaterOrEqual`, `Less`, `LessOrEqual`,
`CompareBy`, `Between`, and `Exclude` filters. For example, queries for the same
store and different regions can reuse the store bitmap:

```go
local := index.Local()
defer local.Close()

queries := []Constraint{
	{StoreUUID: pointer("store-10"), RegionID: pointer(20)},
	{StoreUUID: pointer("store-10"), RegionID: pointer(30)},
}
for _, query := range queries {
	matches = matches[:0]
	local.Search(query, &matches)
	fmt.Println(matches)
}
```

`Local` initially keeps up to two recent intermediate bitmaps per filter node.
Value-based caches can grow to four after repeated misses prove that a larger
working set is being reused; one-off values never cause bitmap growth. This
keeps memory bounded while covering repeated, alternating, and small cyclic
workloads. Call `local.Close()` when the worker is done. It releases cached
query values and returns the internal bitmap context to this index for reuse.
A closed `Local` cannot be used again.

A local search context is not safe for concurrent use. Create one inside each
goroutine while sharing the immutable index:

```go
for range workers {
	go func() {
		local := index.Local()
		defer local.Close()
		var matches []string
		for query := range jobs {
			matches = matches[:0]
			local.Search(query, &matches)
			handle(matches)
		}
	}()
}
```

Use `index.Search` when searches do not have value locality or when maintaining
a per-goroutine context is inconvenient. The index and its regular `Search`
method remain safe for concurrent use.
`Local.Visit` provides the same caching for streaming result iteration.

A complete runnable example is available in
[`examples/local_search`](examples/local_search).

`Visit` avoids collecting results and supports early termination:

```go
index.Visit(value, func(id string) bool {
	return handle(id) // false stops iteration
})
```

If the same external ID is inserted more than once, it is returned at most
once. Its first matching insertion determines result order.

### Rule diagnostics

`Inspect` can mark one rule and report the representation selected during
`Build`. For a `Lossy` rule it also exposes accounted memory usage and limit,
item and distinct-value counts, and lossy bucket granularity. Optional metrics
return an availability flag. Call `Snapshot` once to capture one successful
build generation and its observed runtime counters. Runtime counters are
low-priority samples: shared `Index` searches and 63 of every 64 `Local`
contexts execute the plain tree without instrumentation. The selected Local
accumulates candidate checks, empty results, cache activity, adaptive cache
expansions, and an allocation-free result-cardinality histogram without
atomics, then publishes them on `Close`. Snapshots are monotonic but do not
represent total workload activity and exclude selected Local contexts that are
still open.

## Development

```sh
go test ./...
go vet ./...
```
