# ruleix

`ruleix` is a strongly typed, in-memory rule index for Go. It solves the
inverse lookup problem: given one concrete value, find every stored rule whose
optional constraints match it.

It is useful for pricing, feature flags, promotions, routing, and audience
targeting when the same rule schema is searched repeatedly.

```sql
SELECT * FROM rules
WHERE (store_id = $1 OR store_id IS NULL)
  AND (region_id = $2 OR region_id IS NULL);
```

In a stored rule, a missing value acts as a wildcard. Ruleix compiles these
rules into an immutable index and combines precomputed candidate sets instead
of scanning every rule for every query.

## Features

- Generic, strongly typed API without runtime reflection in search.
- Equality, exclusion, ordered, interval, and stored-operator filters.
- Conjunctions with `All`, including direct access to nested fields.
- Unique results in first-insertion order.
- Immutable indexes that support concurrent searches.
- Batch search with one internally managed cache context per request.
- Memory-bounded conservative indexes through `Lossy`.
- Opt-in build and sampled runtime diagnostics through `Inspect`.

Ruleix requires Go 1.24 or newer.

## Installation

```sh
go get github.com/typomaker/ruleix
```

## Quick start

Define one struct type for both stored constraints and concrete queries. Every
getter returns `(value, ok)`: `ok == false` means that the field is missing.

```go
package main

import (
	"cmp"
	"fmt"

	"github.com/typomaker/ruleix"
)

type Constraint struct {
	Country      *string
	MinimumTotal *int
}

func main() {
	schema := ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(
			func(c Constraint) *string { return c.Country },
		)),
		ruleix.GreaterOrEqual(ruleix.GetterFromPointer(
			func(c Constraint) *int { return c.MinimumTotal },
		), cmp.Compare[int]),
	)

	countryDE, minimum100 := "DE", 100
	constraints := []Constraint{
		{Country: &countryDE, MinimumTotal: &minimum100},
		{Country: &countryDE},
		{},
	}
	ids := []string{"de-orders-over-100", "all-de", "global"}

	index, err := ruleix.New[Constraint, string](schema).Build(
		ruleix.Zip(constraints, ids),
	)
	if err != nil {
		panic(err)
	}

	minimum150 := 150
	query := Constraint{Country: &countryDE, MinimumTotal: &minimum150}
	found := make([][]string, 1)
	index.Search([]Constraint{query}, found)
	matches := found[0]
	fmt.Println(matches)
	// Output: [de-orders-over-100 all-de global]
}
```

Runnable examples cover [multiple columns](examples/multiple_columns),
[repeated local searches](examples/local_search), and
[`Between` with `CompareBy`](examples/between_and_gt).

## Matching semantics

| Filter | A stored constraint matches when |
| --- | --- |
| `Include` | The query value equals the stored value. |
| `Exclude` | The query value does not equal the stored forbidden value. |
| `GreaterOrEqual` | The query value is greater than or equal to the stored value. |
| `LessOrEqual` | The query value is less than or equal to the stored value. |
| `Greater` | The query value is greater than the stored value. |
| `Less` | The query value is less than the stored value. |
| `Between` | The stored interval fully covers the query interval. |
| `CompareBy` | The comparison operator stored with the constraint evaluates to true. |
| `All` | Every child rule matches. |

For ordinary filters, a missing stored value is a wildcard and matches every
concrete query value. A missing query value matches only stored wildcards. Zero
values remain concrete when returned with `ok == true`.

For `Exclude`, a missing stored value forbids nothing. `Between` evaluates:

```text
stored.from <= query.from AND query.until <= stored.until
```

Either stored bound may be a wildcard. `CompareBy` supports `OperatorEQ`,
`OperatorLT`, `OperatorLTE`, `OperatorGT`, and `OperatorGTE`. Its query-side
operator is ignored; the operator belongs to each stored constraint.

## Building an index

`New` returns a reusable builder. `Build` consumes an `iter.Seq2` of constraints
and external IDs. `Zip` is a convenience for parallel slices and panics when
their lengths differ.

```go
builder := ruleix.New[Constraint, string](schema)
index, err := builder.Build(ruleix.Zip(constraints, ids))
```

Keep the builder when periodically rebuilding the same schema. Each successful
call returns an independent immutable index; earlier indexes remain valid. Do
not call `Build` concurrently on the same builder without external
synchronization. A failed build does not publish partial state or prevent a
later build.

When the same external ID appears more than once, its constraints are combined
under that ID. Search returns the ID at most once, ordered by its first
insertion.

## Searching

`Index.Search(conditions, found)` appends matches for `conditions[i]` to
`found[i]`, preserving first-insertion ID order. The outer slices must have
identical lengths; a mismatch panics before changing any result.

```go
conditions := []Constraint{query, nextQuery}
found := make([][]string, len(conditions))
index.Search(conditions, found)

// Reset each destination before the next request if old results are not needed.
for i := range found {
    found[i] = found[i][:0]
}
index.Search(conditions, found)
```

For one condition, pass a one-element slice. Search returns `true` if this call
appends at least one match across the batch, and `false` otherwise, including
empty batches. Existing destination contents do not count. Every condition is
processed even after a match. Inner destination backing arrays must not overlap.

An `Index` is immutable and safe for concurrent calls with independent outputs.
Each call acquires one private cache context and releases it on return; there is
no public `Local` lifecycle. Related conditions can share postings and checks.
See [batch search](docs/search-batch.md) for execution and fallback details and
[API migration](docs/batch-api-migration.md) for the breaking change from v0.9.0
and the experimental `SearchBatch` API.

## Memory-bounded indexes

`Lossy` puts a retained-memory limit around one supported rule or a complete
`All` subtree:

```go
bounded := ruleix.Lossy(
	ruleix.All(
		ruleix.Include(customerID),
		ruleix.GreaterOrEqual(minimumTotal, cmp.Compare[int]),
	),
	ruleix.MemoryLimit(20<<20),
)
```

The exact representation is retained when it fits. Under pressure, Ruleix
selects a coarser conservative representation: search may return false
positives but never omit an exact match. `MemoryLimit` applies to Ruleix's
deterministic retained accounting, not transient heap use or process RSS. Build
returns an error when no supported representation can satisfy the limit.

See [the Lossy index contract](docs/lossy-index.md) for supported value types,
nested limits, accounting, and precision behavior.

## Diagnostics

Wrap a rule with `Inspect` to observe the representation selected by `Build`:

```go
var inspector ruleix.Inspector
observed := ruleix.Inspect(&inspector, bounded)
index, err := ruleix.New[Constraint, string](observed).Build(entries)
snapshot := inspector.Snapshot()
```

Snapshots describe the latest successful build and low-priority sampled runtime
metrics. They are diagnostics, not exact workload accounting. Shared `Index`
searches use the plain search tree unless their internal context is sampled. See the
[inspection API contract](docs/inspect-api.md) for availability and sampling
semantics.

## Documentation

- [Index architecture](docs/index-architecture.md)
- [Lossy index contract](docs/lossy-index.md)
- [Inspection API](docs/inspect-api.md)
- [Optimization decisions](docs/optimization-decisions.md)
- [Performance history](docs/performance-history.md)
- [Changelog](CHANGELOG.md)
- [Roadmap](ROADMAP.md)

## Development

```sh
go test ./...
go test -race ./...
make lint
```
