# Ordered Build and Local planning experiments

The baseline is `2f913e2`. The production fixture contains 96,630 constraint
rows and 1,447 distinct external IDs. The request benchmark has 64 customer
requests of 100 offers each and uses a distinct `Local` per request. In the
main scenario, neighboring offers change one to three store or slot fields.
The rotating control visits the same offers in stride-37 order to increase
store and slot key changes while preserving the fixed customer fields.

Measurements ran on an Apple M1 Max with macOS 26.6.2, Go 1.27.3
(`darwin/arm64`), and `GOMAXPROCS=1`. Baseline and candidate binaries
ran interleaved with the same benchmark sources and data. No dump data
is checked into Git.

## Ordered insertion

On an existing endpoint posting, the baseline updates both the posting and
its block aggregate for every row. Unconditional `CheckedAdd` dropped the
duplicate-heavy 96,000-insertion fixture from 8.91 to 5.93 ms, but slowed the
unique fixture from 3.99 to 4.12 ms. Roaring's `CheckedAdd` reads container
cardinality before and after insertion. Tracking a high-water mark in every
ordered index still cost about 0.34 ms in the all-unique fixture and added
per-index insertion state.

The implemented version reuses the external ID deduplication lookup in Build.
On existing IDs it uses `CheckedAdd` for unquantized ordered operands and
updates the aggregate when the posting gains a member. Known-new IDs retain
ordinary insertion. The flag belongs to the current Build, and rules drop its
reference on finalization. `Between` uses it for both endpoints. Quantized
ordered insertion and `CompareBy` keep their previous insertion path.

In the focused 400 ms runs, the duplicate fixture moved from 8.90 to 6.05 ms
with 624 allocations on both revisions. The unique fixture took 3.99 vs
4.09 ms with 1,607 allocations on both. The full dump confirmed the benefit.
The production-shape Build control uses 38,098 unique external IDs.

## Small Between postings

The first trial skipped clones below a posting-cardinality threshold and
saved about 180 allocations per Build. Roaring's accounting exposes logical
container sizes. It cannot establish whether the skipped clones would have
released excess backing capacity for every small posting.

The implemented version still trims the small dictionary's item pointer slice
when it has excess capacity. It defers bitmap cloning until interning chooses
a new canonical posting. The interner preserves the existing fingerprint and
equality checks, source IDs, and order. When a prior canonical instance matches,
the previous eager clone was discarded anyway. The isolated full-dump trial
reduced transient Build allocation by roughly 14–16 KiB and 91–92 allocations.
Retained index size stayed near 2.58 MB. The result and range layouts published
to Search use the same canonical postings as before.

## Include planning lookup

The initial last-key memo stored typed records in each request's node cache.
On the main dump workload it improved average latency, but raised the measured
request cost from 921 B and 17 allocations to 1,049 B and 21 allocations.
Allocation profiles located the extra objects in the typed memo creation
inside the equality planning lookup.

Reusing typed memo objects in the recyclable Local pool removed the
steady-state allocation increase. A single request on a fresh pool still
allocated 32 objects against 27 on the baseline. Full allocation profiles
located the five new objects in the planning slot slice and four typed
memo records. The smaller pool dropped first-request bytes from 4,944 to
4,496, but the extra allocation count excluded that design.

The next prototype embedded eight direct-mapped memo slots in ordinary
Local storage. Entries held the last key and immutable posting for an exact
Include node. Build selects the key representation for strings and 16-byte
arrays of bytes, including named types. The stored key uses exact equality.
Slot collisions cause replacement. Other key types, lossy equality, wildcard
postings, and sampled inspection use the existing lookup path. Closing the
Local clears key values and bitmap references. Sampled pools allocate their
first inspector counter chunk with the pool and omit the memo slots.

With 5,000 live fresh requests, the eight-slot prototype used 4,201 vs
4,844 B/request and 31 allocations on both. In three interleaved 90,000-request
runs it improved the median mean from 55.98 to 54.23 µs and median p99 from
90.79 to 89.13 µs. Median p99.9 rose from 119.92 to 123.00 µs. The
45,000-request series also had a higher median p99.9, 116.42 vs 124.46 µs.

A final prototype kept one inline slot for the leading operand in the current
Local plan. The mixed mean moved from 57.58 to 60.08 µs and p99.9 from
148.38 to 152.50 µs in three 60,000-request runs. The rotating control moved
from 59.99 to 62.19 µs and p99.9 from 148.63 to 163.42 µs in three
40,000-request runs. In the 70,000-request CPU profiles, plan refresh used
1.24 s on the baseline and 1.27 s in the one-slot candidate. The memo
variants traded ordinary posting lookup for typed dispatch and key
comparison. The aggregate profile does not localize the p99.9 increase to
one function, so the tail effect remains open. The shipped change keeps
the baseline Local planner.

## Build-only checkpoint

The combined Build change lowered full-dump Build from 166.51 to 152.37 ms
in the alternate 12-build runs. Allocation traffic fell from about
4.696 to 4.682 MB per Build and from about 46,458 to 46,366 allocations.
Live heap remained near 2.58 MB per index. The unique production-shape
fixture took 34.97 vs 34.86 ms and 30,297 allocations on both revisions.
The focused insertion control used 500 ms runs and measured 9.32 vs
6.15 ms for duplicates and 4.28 vs 4.38 ms for unique insertions.
Allocation counts were 624 and 1,607 respectively on both revisions.
In matched 20-Build profiles, cumulative `Bitmap.Add` samples dropped
from 1.16 to 0.89 s. The candidate sampled an additional 0.15 s in
`CheckedAdd`. The timed Build heap and allocation counts are reported
above because the process allocation profile also includes dump parsing.

The initial 45,000-request sweep showed 56.38 vs 56.50 µs mean and
116.54 vs 123.02 µs p99.9. The 30,000-request rotating sweep had an
outlying baseline p99.9 of 265.38 µs. In three isolated runs of 80,000
ordinary requests per revision, the p99.9 difference did not reproduce.

| Local request metric | Baseline | Build-only change |
| --- | ---: | ---: |
| Mean | 56.39 µs | 56.41 µs |
| p99 | 90.92 µs | 91.21 µs |
| p99.9 | 119.92 µs | 120.21 µs |
| Allocated bytes | 921 | 921 |
| Allocations | 17 | 17 |

Paired 60,000-request CPU and allocation profiles measured p99.9 of
122.58 vs 115.71 µs. The same search executor and plan refresh were the
leading CPU paths. The cold and warm joined Local controls were
1,236 vs 1,242 µs and 240 vs 237 µs per batch. The lossy Local control
took 494.6 vs 495.6 ns per lookup and returned 358 candidates on both
revisions. Exact Local cache hits remained near 193 ns with zero
allocations. The representative Index controls preserved their
allocation counts and lossy candidate count.

`go test -covermode=count -coverprofile=coverage.out ./...`,
`go vet ./...`, and `git diff --check` passed. Coverage was 39 of 39
changed executable production lines.

## Commands

Compile each revision with the same benchmark sources. When copying the
ordered probe to the baseline, replace the call to
`insertWithDuplicateCheck(id%60, uint32(id), row >= mode.ids)` with
`insert(id%60, uint32(id))`. The request and Build benchmarks can be copied
directly. Run the paired binaries alternately.

```bash
export GOMAXPROCS=1
export RULEIX_PRODUCTION_DUMP=/path/to/production-dump.json

go test -c -o ruleix.test .
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime 45000x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime 80000x -test.benchmem
RULEIX_PLANNER_ROTATE=1 ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime 35000x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpFreshLocal100$' -test.benchtime 5000x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDump(Build|Retained)Probe$' -test.benchtime 12x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionShapeBuild$' -test.benchtime 450ms -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkOrderedInsertProbe$' -test.benchtime 400ms -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProduction(JoinLocal|ShapeSearch|ShapeLossySearch|ShapeExactCacheEntryHit|ShapeEqualityOnlySearch)$' -test.benchtime 220ms -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProduction(ShapeLocalRetainedMemory|JoinLocalRetainedMemory)$' -test.benchtime 25x -test.benchmem
```

Use `-test.cpuprofile` and `-test.memprofile` for paired runs. The full
Build profile included JSON parsing during setup. In the profile of the
high-water candidate, cumulative bitmap `Add` samples dropped from 1.72 s
to 1.29 s and the ordered insertion path from 0.85 s to 0.60 s. In the
first lossy Local sweep, short runs suggested a slowdown. Isolated alternating
2 s profiles measured 494.4 vs 496.8 ns/op, zero allocation on both, and
the same result counts. The hot path was exact query-cache hit validation,
which bypasses the Include planning memo.
