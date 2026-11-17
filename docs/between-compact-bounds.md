# Compact Between bounds under All

After bitmap interning, Build can publish a compact layout for a Between
operand of All. Both endpoint indexes must have at least eight blocks.
Only blocks with multiple keys and one ID per posting are eligible for
compaction. Each such block keeps its aggregate bitmap and stores its
individual posting IDs as integers.

Each side has an array indexed by internal ID with positions in the
physical endpoint index. The lower side keeps the first position and the
upper side the last. These positions reflect the comparator order and any
Lossy key merges. Wildcards are tested independently. All uses its existing
estimates, candidate threshold, and ranking.

Whole blocks still use their existing aggregate bitmaps. For a partial
compact block, search includes the aggregate and removes out-of-range IDs
whose best position also fails the query. A block whose IDs are consecutive
and whose positions uniquely own the effective endpoint can remove the range
in one operation. This preserves matches supplied by another posting or
by a wildcard. Estimates still count postings at the boundary and use block
prefix counts for complete blocks.

The interner counts singleton bitmap owners while building an eligible
index. A singleton shared with another node contributes no savings unless
all its owners are removed by this compaction. The gate compares removable
bitmap headers to the added table storage. It separately checks the removed
serialized posting sizes against the same added storage. This second check
ensures compaction fits the existing Lossy accounting upper bound. Inspector
snapshots retain their conservative budget estimates recorded before
compaction. Nodes that fail the gate retain the original representation.

## Verification

Baseline revision `1358f15b394bccb006b24066d133491bc01e16aa`. The
candidate is the implementation in this checkpoint. Apple M1 Max,
Darwin arm64, Go 1.27.3.

The focused benchmark alternates baseline and compact builds in each binary
invocation. Both use the same Build pipeline with the compaction switch varied.
The query rotates equality keys and partial boundary positions over 32,768
distinct intervals. Broad uses 8,192 equality candidates. The retained
benchmark compares live heap after GC over three retained indexes per run.

```sh
go test -c -o /tmp/ruleix-between-release.test
for run in 1 2 3 4 5; do
  /tmp/ruleix-between-release.test -test.run '^$' \
    -test.bench '^BenchmarkCompactBetween$' -test.benchtime=180ms
done
/tmp/ruleix-between-release.test -test.run '^$' \
  -test.bench '^BenchmarkCompactBetweenRetained$' -test.benchtime=3x -test.count=3
```

| Path | Baseline latency | Compact latency | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Index, 1 candidate | 5.330 us | 0.207 us | 32 → 32 | 2 → 2 |
| Index, 8 candidates | 79.600 us | 0.515 us | 48 → 48 | 3 → 3 |
| Local, 1 candidate | 5.678 us | 0.166 us | 8 → 0 | 0 → 0 |
| Local, 8 candidates | 82.567 us | 0.424 us | 124 → 0 | 2 → 0 |
| Index, broad | 140.191 us | 138.989 us | 82,461 → 79,418 | 22 → 22 |
| Local, broad | 224.507 us | 223.539 us | 89,326 → 87,309 | 31 → 27 |
| Index, broad shuffled | 142.664 us | 140.833 us | 84,827 → 79,245 | 22 → 22 |
| Local, broad shuffled | 217.223 us | 210.311 us | 89,437 → 79,053 | 32 → 26 |
| Build | 50.446 ms | 48.592 ms | 48,374,016 → 42,751,416 | 1,013,777 → 747,549 |

Retained heap changed from 10,754,995 to 6,562,851 bytes per index
(median of three). The initial compact implementations increased broad
search allocations when adding accepted singleton IDs back to the result
or building a scratch bitmap. Reusing the whole boundary aggregate and
trimming excess IDs avoided both sources of temporary work.

For the broad shuffled Index path, separate one-second CPU and allocation
profiles used `-test.bench '^BenchmarkCompactBetween$/^BroadShuffled$/^Index$/^Baseline$'`
and the corresponding `^Compact$` pattern with `-test.cpuprofile` and
`-test.memprofile`. Both CPU profiles were dominated by Roaring
intersection work. The profiled paths used 22 allocations per operation.

Standalone Between and production-shaped Exact and Lossy controls were
also run against binaries compiled from the baseline and from the
contiguous-only iteration of this change. The final exclusive-block
shortcut only affects compact blocks. The control pattern was
`^Benchmark(BetweenManyUniqueBounds|BetweenNarrowIntersection|LocalBetweenReuse|BetweenSelectiveSide|ProductionShapeSearch|ProductionShapeLossySearch)$`
with three interleaved 100 ms pairs. Production Exact Index measured
28.61 → 28.44 us and 28 allocs/op on both builds. Production Lossy Index
measured 161.82 → 160.33 us and 25 allocs/op on both builds. Longer
250 ms alternating pairs for the standalone Cycle4 Index and production
Lossy Local controls measured 2.56 → 2.51 us and 483.7 → 498.2 ns
respectively. The latter samples overlapped (458–506 ns baseline,
453–513 ns candidate), with zero allocations and identical output counts.

Differential tests cover exact and lossy builds, sorted and shuffled
bounds, repeated IDs, missing bounds, inverted intervals, and shared
singletons. They compare cardinality estimates and Index and Local
results against the uncompressed layout.

The full suite passed with coverage enabled. Go coverage blocks
intersecting changed production lines covered 207 of 213 statements (97.2%).
The focused compaction tests also passed with the race detector using
`go test -race -run '^(TestCompactBetweenMatchesBaseline|TestBetweenCompactionKeepsSharedSingletons)$' .`.
