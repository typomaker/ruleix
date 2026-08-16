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

For example, given an order from a gold customer in Germany, you may need to
find every promotion that applies to its country, customer tier, total, and
sales channel. Some promotions constrain every field, while others apply to an
entire country or globally.

A straightforward implementation scans every rule for every request and checks
each field individually. This is simple, but its cost grows with both the number
of rules and the number of fields—especially when rules contain wildcards,
ranges, exclusions, or nested properties.

`ruleix` builds an immutable index from the rules once. Searches then combine
precomputed candidate sets for each constraint instead of evaluating every rule
from scratch.

## Features

- Generic, strongly typed API with no reflection.
- Equality, exclusion, ordered, interval, and dynamic comparison filters.
- Wildcards: a `nil` field in a stored constraint matches every concrete value.
- Multi-column rules composed with `All` and nested getters built with `Path`.
- Unique results in first-insertion order.
- Immutable indexes safe for concurrent searches after `Build`.
- Roaring bitmap-backed candidate sets with pooled search scratch space.

## Requirements

- Go 1.23 or newer.

## Installation

```sh
go get github.com/albertsultanov/ruleix
```

## Quick start

This example indexes rules across four different columns: country, customer
tier, minimum order total, and an excluded sales channel.

```go
package main

import (
	"cmp"
	"fmt"

	"github.com/albertsultanov/ruleix"
)

type Constraint struct {
	Country         *string
	Tier            *string
	MinimumTotal    *int
	ExcludedChannel *string
}

func pointer[T any](value T) *T { return &value }

func main() {
	schema := ruleix.All(
		ruleix.Include(func(c Constraint) *string { return c.Country }),
		ruleix.Include(func(c Constraint) *string { return c.Tier }),
		ruleix.GreaterOrEqual(func(c Constraint) *int { return c.MinimumTotal }, cmp.Compare[int]),
		ruleix.Exclude(func(c Constraint) *string { return c.ExcludedChannel }),
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

	entries, err := ruleix.Zip(constraints, ids)
	if err != nil {
		panic(err)
	}
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
because the operator is part of each stored rule; for a fixed operator, use
`Greater` or `Less` directly.

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
| `CompareBy` | the operator stored in the rule evaluates to true |
| `Path` / `Path3`–`Path5` | compose getters to access a nested property |
| `All` | every child rule matches |

All value getters return pointers. In a stored constraint, `nil` is a wildcard.
In a search value, `nil` only matches stored wildcards. For `Exclude`, a stored
`nil` means that no value is forbidden.

`Between` applies the following rule; either stored bound may be a wildcard:

```text
stored.from <= query.from AND query.until <= stored.until
```

`CompareBy` supports `=`, `<`, `<=`, `>`, and `>=`. The operator belongs to the
stored rule; operator text in the search value is ignored. `Build` rejects an
invalid operator without returning a partially built index.

`Path` composes pointer getters and propagates `nil`, so the concrete filter
keeps control of wildcard behavior. Calls can be nested to traverse any depth:

```go
ruleix.Include(ruleix.Path3(
	func(c Constraint) *Platform { return c.Platform },
	func(p Platform) *Version { return p.Version },
	func(v Version) *int { return &v.Major },
))
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

`Zip` is a convenience for parallel slices and returns an error when their
lengths differ.

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

`Search` reuses the capacity of a destination slice:

```go
var matches []string
index.Search(value, &matches)
```

When adjacent searches repeat constraint values, `Local` can reuse intermediate
bitmap results from `Include`, `Greater`, `GreaterOrEqual`, `Less`, `LessOrEqual`,
`CompareBy`, `Between`, and `Exclude` filters. For example, queries for the same
store and different regions can reuse the store bitmap:

```go
local := index.Local()

queries := []Constraint{
	{StoreUUID: pointer("store-10"), RegionID: pointer(20)},
	{StoreUUID: pointer("store-10"), RegionID: pointer(30)},
}
for _, query := range queries {
	local.Search(query, &matches)
	fmt.Println(matches)
}
```

`Local` keeps up to two recent intermediate bitmaps per filter node. This makes
memory use bounded while covering repeated and alternating values. The cached
bitmaps remain allocated until the `Local` becomes unreachable.

A local search context is not safe for concurrent use. Create one inside each
goroutine while sharing the immutable index:

```go
for range workers {
	go func() {
		local := index.Local()
		var matches []string
		for query := range jobs {
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

## Development

```sh
go test ./...
go vet ./...
```
