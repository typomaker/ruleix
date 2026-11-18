# Batch-only search API

## Contract and migration

`Index.Search(conditions []C, found [][]ID)` is the sole public search method.
It replaces experimental `SearchBatch` without changing its execution strategy.
The single-condition `Index.Search`, public `Local` type, `Index.Local`, and
`Local.Search`/`Close` have been removed. This is a source-breaking unreleased
change relative to v0.9.0 and acaa3db; no release or remote push is part of it.

Allocate exactly one destination slot per condition:

```go
conditions := []Condition{firstOffer, secondOffer}
found := make([][]ID, len(conditions))
index.Search(conditions, found)
```

Matches append to the corresponding inner slice, preserving unique IDs and
first-insertion order. Existing destination contents remain. Reset each inner
slice when reusing buffers for another request. Backing arrays must not overlap.
Different calls may run concurrently with independent destinations.

An empty batch does no work. Unequal outer lengths panic before any result
mutation. There is no boolean return; inspect each output or compare its length
before and after the call to detect newly appended results. For one query, use
`[]Condition{query}` and a one-element `[][]ID` destination. Replace calls to
`SearchBatch(conditions, found)` directly with `Search(conditions, found)`.
Replace a Local loop with one Search call over the complete request.

Each nonempty call owns one private stack-held `localContext`; it is released
on return. Short batches and unsupported/observed shapes use the original
single-query kernel with that same context. Native-mask planning, chunk limits,
posting order, exclusions, telemetry sampling and pool reset are unchanged.
Build and published posting representations are unchanged. Callers no longer
control cache lifetime across separate requests or select the old shared-scratch
single-query path. One buyer and all of that buyer's offers belong in one call.

Legacy single-query and lifecycle tests use explicitly named test-only bridges
(`SearchOne`, `TestLocal`, `TestContext`), absent from normal builds. They retain
coverage and comparison controls without exposing the old production API.
Batch contract, differential, fallback and concurrent tests exercise Search.
All runnable examples use Search; their output is now verified by Go examples.
`go doc Index` confirms the normal method surface in
[api-surface.txt](experiments/batch-api-migration/api-surface.txt).
Historical benchmark snapshots remain pinned to their original APIs.

## Comparable verification

Baseline: `acaa3db032e73412903fa0840ab6c7675119f0de` SearchBatch.
Candidate: this task's batch-only API, with exact production-source hashes in
[metadata.json](experiments/batch-api-migration/metadata.json). The report and
source are committed together; no unrelated production edits were included.

Environment: Apple M1 Max, darwin/arm64, macOS 26.6.2, Go 1.27.3,
`GOMAXPROCS=1`. Supplied full dump: 96,630 rows, SHA256
`ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.
The schema and generated workloads are the same acaa3db fixtures on both sides;
only method/type selectors change for compilation. No field-name heuristics or
customer-specific assumptions were introduced.

Across 13 workload modes, both binaries and both Local/kernel controls produce
identical ordered output digests: 697 requests, 69,700 offers, 11,470 matches,
SHA256 `d3b9ad65d45b8d15577575f445011a672c480c21cf7431a67fb884be3d17c7f6`.
Zero-result cohorts use unsuitable queries against the populated full index.

Primary measurements interleave old/new/new/old/old/new with 40,000 complete
requests per sample. Each request has 100 offers and independently reused output
buffers. Lifecycle, analysis and output appends are timed. Table values are
medians of three samples; Go ns/op also includes common clock/reset overhead.

| Cohort | Mean old → new, µs | p99.9 old → new, µs |
| --- | ---: | ---: |
| Mixed | 12.371 → 12.397 | 29.500 → 30.334 |
| Missing customer | 1.492 → 1.495 | 1.708 → 1.708 |
| Missing store | 1.550 → 1.552 | 1.750 → 2.041 |
| Missing slot | 1.425 → 1.429 | 1.625 → 1.625 |
| Existing keys, empty intersection | 4.246 → 4.280 | 10.750 → 12.291 |
| 1–3 candidates | 7.128 → 7.170 | 17.958 → 17.375 |
| Broader candidate groups | 15.950 → 15.785 | 35.792 → 35.292 |
| Unrelated offers | 61.154 → 61.372 | 107.334 → 99.500 |

All primary rows have 0 B/op and 0 allocs/op after warm-up on both revisions.
These numbers support retaining the existing performance, not a new speedup
claim. The original Local-versus-batch improvement remains in [batch search](search-batch.md).
Two samples per revision also cover 0/1/4/16/100 offers across mixed, all four
empty cohorts, and unrelated offers; allocation classes agree throughout.
Short fallback batches may allocate just as before (mixed one/four offers:
783 B, 10 allocations; unrelated four offers: 1,058 B, 24 allocations).

## Tail investigation and limits

The initial empty-store/intersection tails warranted a larger interleaved
old/new/new/old/old/new check at 400,000 operations per case. Mean store latency
was 1.557 → 1.554 µs and p99.9 2.000 → 1.917 µs. Empty intersection mean was
4.259 → 4.263 µs and p99.9 11.334 → 11.791 µs. This does not establish a tail
improvement. A paired million-operation CPU/allocation profile measured
intersection mean 4.263 → 4.312 µs, while p99.9 reversed to 12.375 → 11.708 µs.
There is no consistent directional tail change across these checks.

Both CPU profiles show the same posting lookup/native-mask work, with no added
search phase. Allocation profiles include dump decoding/Build and benchmark
sample storage; no new per-request allocation class appeared. The old/new
compiled core has identical operation sequences and counts for acquisition,
release, dispatch, grouping and fallback. Operands differ at relocations and
Go generic dictionary offsets after removing methods; this is not a claim of
byte-identical binaries or a proof about latency. Index/context fields and
recycling logic are unchanged, so this migration adds no retained structure.

At over 100,000 measured operations, the existing timing fixture grows its
sample slice: 400,000-operation checks report 38 B/op and profiled million runs
43 B/op on both revisions, with 0 rounded allocs/op. That is benchmark sample
storage, not a new search allocation. Raw profiles include setup and are
supporting diagnostics, not search-only attribution. Tail estimates on this
shared developer machine have scheduling/profiling noise; no production p99.9
SLA or new tail speedup is inferred.

## Reproduction and checks

```sh
RULEIX_PRODUCTION_DUMP=/path/to/full-dump.json \
  python3 docs/experiments/batch-api-migration/run.py
python3 docs/experiments/batch-api-migration/assembly.py \
  "$(cat /tmp/ruleix-batch-api-last)"
go test ./... -coverprofile=/tmp/ruleix-api-cover.out
python3 docs/experiments/batch-api-migration/diff-coverage.py /tmp/ruleix-api-cover.out
go test -race ./...
GOTOOLCHAIN=go1.26.0 /tmp/ruleix-release-tools/golangci-lint run
git diff --check
```

The lint binary is golangci-lint 2.12.2; Go 1.26 is used for its supported
importer. The implementation/tests and performance binaries use Go 1.27.3.
Coverage is 21/21 changed executable production statements (100%), including
runnable examples; root package aggregate coverage is 92.8%. Full tests, race,
normal builds and lint pass.
The two modified preexisting oversized test files were split into focused
modules; all changed files meet the 500-line limit. Raw measurements, coverage,
API listing and diagnostic profile summaries are in
[the evidence directory](experiments/batch-api-migration/README.md).
