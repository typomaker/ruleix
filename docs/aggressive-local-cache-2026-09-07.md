# Aggressive Local cache policy

## Decision

`Index.Search` is the controlled-memory search path. `Local.Search` is the
explicit memory-for-speed path and may retain memory proportional to cached
result sizes. The Local caches therefore use their existing fixed entry counts
without aggregate byte budgets:

- child caches admit a value after its second recent use and retain two entries,
  adapting to four under sustained reuse pressure;
- each `All` plan retains four exact results;
- every retained exact result stores compact internal IDs, regardless of
  cardinality;
- Local scratch pools reuse wide bitmaps;
- the shared `Index.Search` pool still discards bitmaps above 64 KiB.

The removed 64 KiB result and plan budgets, 1 MiB child budget, and 512-ID
cutoff created size-dependent warm-search cliffs. Fixed slot counts still bound
cache multiplicity, but bytes per Local now scale with index cardinality and
schema width. Concurrent consumers multiply that retained memory by their live
Local count. Callers that prioritize memory use `Index.Search`.

## Cardinality boundary

Environment: Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`. Baseline is
`02f95f3`; candidate is the working tree for this change. Baseline and candidate
test binaries used `-benchtime=200–500ms` and three or five repetitions.

| Matches | Baseline Local | Candidate Local | Candidate allocation |
| ---: | ---: | ---: | ---: |
| 512 | 297 ns/op | 317 ns/op | 0 B/op, 0 allocs/op |
| 513 | 1,355 ns/op | 305 ns/op | 0 B/op, 0 allocs/op |
| 1,024 | 2,643 ns/op | 566 ns/op | 0 B/op, 0 allocs/op |
| 2,048 | 5,121 ns/op | 1,121 ns/op | 0 B/op, 0 allocs/op |
| 4,095 | 10,028 ns/op | 2,189 ns/op | 0 B/op, 0 allocs/op |

The former 512-to-513 transition was a 4.6x discontinuity. Direct compact-ID
output remains approximately 4.5x faster through 4,095 matches. The 512 result
is within short-run noise and remains allocation-free.

Reproduce the candidate boundary:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkWarmLocalResultCardinality/Cardinality(512|513|1024|2048|4095)$' \
  -benchmem -benchtime=200ms -count=3 .
```

## Wide result

The wide fixture has 500,000 entries and 250,000 alternating matches. Its
bitmap exceeds the former 64 KiB exact-result budget.

| Metric | Baseline | Candidate |
| --- | ---: | ---: |
| Warm latency | 798 us/op | 132 us/op |
| Search allocation | 124,921 B/op, 43 allocs/op | 0 B/op, 0 allocs/op |
| Retained memory | 2,344 B/Local | 1,076,008 B/Local |

The candidate is approximately 6.0x faster and removes repeated materialization
allocations by deliberately retaining about 1.03 MiB more per warmed Local.

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkWarmLocalWideResult(RetainedMemory)?$' \
  -benchmem -benchtime=200ms -count=3 .
```

Use `-benchtime=5x` for the retained-memory case.

## Production controls

Five 500 ms runs showed no production-shaped regression:

| Gate | Baseline median | Candidate median |
| --- | ---: | ---: |
| Warm `Local.Search` | 225.2 ns/op | 221.3 ns/op |
| Parallel batches | 236.5 ns/search | 231.1 ns/search |
| Warm retained memory | 92,432 B/Local | 92,208 B/Local |
| Adaptive retained memory | 111,056 B/Local | 110,816 B/Local |

Search allocation classes were unchanged. The small retained decrease comes
from removing byte-accounting fields; this production result cardinality was
already below the old thresholds.

## Verification

The complete `go test ./...` and `go test -race ./...` suites passed. Coverage
over the 14 added or modified executable production lines is 100%; repository
statement coverage for the main package is 91.1%. `git diff --check` passed.
`make lint` still reports 37 pre-existing findings; the baseline reports the
same findings plus two suppressions added here for the unchanged query-key
validation structure. No new lint finding is introduced by this change.
