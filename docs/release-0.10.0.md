# Ruleix v0.10.0 release

This release replaces the single-condition search API with batch-only
`Index.Search(conditions []C, found [][]ID)`. The minor version increases
because this is an incompatible change within the 0.x series.

## Migration

Public `Local`, `Index.Local`, `Local.Search`/`Close`, the old single-condition
`Index.Search`, and experimental `SearchBatch` are removed. Submit all offers
for one buyer in a single Search call. Allocate one destination slot per
condition; matches append to that slot and preserve first-insertion ID order.
Existing destination contents survive. Unequal outer lengths panic before
mutation; an empty batch does no work. Use independent destination backing
arrays, and reset inner slice lengths before reusing results for another request.

```go
found := make([][]ID, len(conditions))
index.Search(conditions, found)
```

For a single condition, pass a one-element batch and destination. Each call
manages its own private cache context. See the full
[API migration contract](batch-api-migration.md) for concurrency and lifecycle.
The Go module path remains `github.com/typomaker/ruleix`.

## Search changes and measurements

The release includes staged native-mask batch execution and a Build-prepared
bounded source for eligible 5–15-condition batches, with early exact-key miss
proofs. Unsupported shapes retain the private single-condition kernel.
Implementation and fallback evidence are maintained in
[batch search](search-batch.md) and [planner experiments](batch-planner.md).

The [direct v0.8.2 comparison](production-v082-short-batch-comparison.md)
measures production revision 9c95f7d, unchanged by release documentation.
On the full 96,630-row dump, mixed 5/15/100-condition means fall from
8.658/16.773/74.365 to 2.607/3.891/12.401 microseconds. At 100 conditions,
p99.9 falls from 170.625 to 31.375 microseconds. All five miss cohorts improve.
All current cases report zero warmed allocations. Ordered results and append
semantics agree in 45 cases. These are local warmed request measurements,
not service latency guarantees; the report retains environment and raw evidence.

## Release verification

On 2026-11-18, the unchanged production tree at 2439171 passed:

- `go test ./...` and `go test -race ./...` with Go 1.27.3 on darwin/arm64
  (valid cached results).
- `GOTOOLCHAIN=go1.26.0 /tmp/ruleix-release-tools/golangci-lint run`,
  golangci-lint v2.12.2: zero issues. Go 1.26 avoids the linter importer's
  unsupported Go 1.27 export data.
- `go doc Index`: Search is the sole public Index method.
- `git diff --check` and the 500-line limit for all release-edited files.

Existing production coverage and differential verification remain documented
in the migration and planner reports. This release task changes documentation
only, so new production diff coverage is not applicable.
The release commit and annotated v0.10.0 tag are pushed atomically to origin;
remote main and the peeled tag must resolve to the release commit.
