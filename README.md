# ruleix

`ruleix` is a strongly typed, in-memory rule index for Go. It indexes a set of
constraints once and efficiently finds every rule that matches a concrete
value.

Use it when you have many rules with optional fields—for example pricing,
feature flags, promotions, routing, or audience targeting—and need to evaluate
the same schema repeatedly.

## Features

- Generic, strongly typed API with no reflection.
- Equality, exclusion, ordered, interval, and dynamic comparison filters.
- Wildcards: a `nil` field in a stored constraint matches every concrete value.
- Multi-column rules composed with `All` and `Nested`.
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
| `Nested` / `Optional` | the nested child rule matches |
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

## Building an index

`New` returns a single-use builder. `Build` accepts an `iter.Seq2` of constraints
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

## Searching

`Search` reuses the capacity of a destination slice:

```go
var matches []string
index.Search(value, &matches)
```

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
