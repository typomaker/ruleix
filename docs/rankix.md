# Rankix design note

`rankix` is a proposed ranked-result layer built on top of `ruleix`. It keeps
ranking outside the bitmap matching engine: `ruleix` indexes a unique ID for
each stored constraint, while `rankix` maps those constraint IDs back to the
caller-visible ID and removes duplicates.

## Data model

Different constraints may produce the same caller-visible ID and have different
priorities:

```text
constraint ID 101 -> ID 10, StoreUUID=123, RegionID=5, rank 10
constraint ID 102 -> ID 10, StoreUUID=123,             rank 9
constraint ID 103 -> ID 10, wildcard,                  rank 1
```

The build path buffers the entries, stably sorts them from highest to lowest
priority, assigns constraint IDs in that order, and builds a
`ruleix.Index[C, ConstraintID]`. Because `ruleix` returns IDs in first-insertion
order, its result is already ranked and searches do not need to sort.

The rank can therefore be implicit in the constraint ID. The persistent sidecar
only needs an array mapping each constraint ID to its caller-visible ID:

```go
constraintToID []ID
```

`Search` walks matching constraint IDs in order, maps them to caller-visible
IDs, and skips IDs already emitted by the current call. Thus the position of a
caller-visible ID is determined by its highest-ranked matching constraint.
`Match` uses the same traversal with a limit of one and stops immediately after
the first result.

## Proposed API

The exact builder representation remains open, but the search surface should
mirror `ruleix`:

```go
func (ix *Index[C, ID]) Search(value C, dst *[]ID) (ok bool)
func (ix *Index[C, ID]) Match(value C, dst *[]ID) (ok bool)

func (local *Local[C, ID]) Search(value C, dst *[]ID) (ok bool)
func (local *Local[C, ID]) Match(value C, dst *[]ID) (ok bool)
```

Both methods append to `dst`. Their return value describes only the current
call and is unaffected by elements already present in `dst`. `Search` appends
all unique caller-visible IDs in rank order; `Match` appends at most one.

Equal priorities retain input order. Without an explicit ranking comparator,
input order is the priority order.

## Tradeoffs

This separation keeps ranking and caller-ID deduplication out of `ruleix`, but
does not eliminate their fundamental storage cost. Since every constraint has a
unique ID in the underlying index, posting-list cardinality depends on the
number of constraints rather than the number of unique caller-visible IDs.
Build also needs temporary storage for entries so it can sort them once.

The benefit is that query-time sorting is avoided, `Match` can stop at the first
bitmap result, and `ruleix` does not need to know about ranking semantics.
