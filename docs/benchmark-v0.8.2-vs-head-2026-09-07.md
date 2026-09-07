# `v0.8.2` versus `HEAD` production benchmarks

Measured on 7 September 2026. The baseline is release tag `v0.8.2`
(`7f32ddc6ccf6b12419a1efb5af1424ff64353cc0`); the candidate is `HEAD`
(`66a7c643d9151cf04111a57038bc084304f8eb32`).

## Outcome

The initial candidate improved uncached `Index.Search` latency by 9.58%, but
warm `Local.Search` regressed by 38.07% and parallel Local by 36.22%. A
performance bisect and CPU profiles attributed the regression to the unused
ID-chunk experiment introduced in `1dee1c1`. Removing the experiment restores
both Local paths to release parity and preserves an 11.27% Index improvement.

The final candidate has neutral Build latency, unchanged search allocation
classes, a 0.17% larger retained index, and unchanged cold, warm, adaptive, and
adversarial Local retained-memory classes relative to `v0.8.2`.

## Comparable results

The latency/build matrix used seven interleaved baseline/candidate process
pairs, 500 ms per benchmark. Values are `benchstat` medians with its 95%
confidence interval and Mann-Whitney U-test result.

| Scenario | `v0.8.2` | `HEAD` | Change |
| --- | ---: | ---: | ---: |
| `Index.Search` | 33.72 µs ±3% | 30.49 µs ±8% | **-9.58%**, p=0.001 |
| warm `Local.Search` | 226.7 ns ±0% | 313.0 ns ±1% | **+38.07%**, p=0.001 |
| parallel Local | 238.8 ns/search ±1% | 325.3 ns/search ±1% | **+36.22%**, p=0.001 |
| `Build` | 37.16 ms ±1% | 38.14 ms ±1% | +2.62%, p=0.001 |

| Scenario | `v0.8.2` | `HEAD` | Change |
| --- | ---: | ---: | ---: |
| `Index.Search` | 40,805 B; 28 allocs | 40,805 B; 28 allocs | unchanged |
| warm `Local.Search` | 0 B; 0 allocs | 0 B; 0 allocs | unchanged |
| parallel Local batch | 201 B; 1 alloc | 268 B; 1 alloc | +67 B; allocations unchanged |
| `Build` | 5,201,452 B; 30,283 allocs | 5,208,412 B; 30,298 allocs | +0.13%; +0.05% |

The retained-memory matrix used seven interleaved pairs, 10 indexes per
retained-index sample and three Locals per Local sample.

| Retained object | `v0.8.2` | `HEAD` | Change |
| --- | ---: | ---: | ---: |
| index | 1.260 MiB ±0% | 1.262 MiB ±0% | +0.17%, p=0.001 |
| cold Local | 2,968 B ±0% | 2,968 B ±0% | unchanged |
| warm Local | 92,432 B ±0% | 92,432 B ±0% | unchanged |
| adaptive Local | 111,056 B ±0% | 111,056 B ±0% | unchanged |
| adversarial Local | 72.53 KiB ±0% | 72.53 KiB ±0% | unchanged, p=0.674 |

## Regression reproduction

Project policy requires a candidate regression to be checked against its
immediate parent. Seven additional interleaved 1-second pairs compared parent
`f0b0d685` with `66a7c643`; all exact production search metrics were neutral:

| Scenario | parent | `HEAD` | Statistical result |
| --- | ---: | ---: | ---: |
| `Index.Search` | 30.37 µs ±2% | 30.37 µs ±3% | p=0.805 |
| warm `Local.Search` | 312.4 ns ±2% | 312.4 ns ±2% | p=0.734 |
| parallel Local | 327.8 ns/search ±4% | 328.0 ns/search ±2% | p=0.972 |

Bytes and allocation counts were also unchanged. Therefore the final strict
equality antonym component commit did not introduce the release-relative
regression. This comparison does not localize which earlier post-release
change introduced it, and no causal claim is made.

## Root cause and removal

A threshold-only performance bisect from `v0.8.2` to the regressed state used
the identical `BenchmarkProductionShapeSearch/Local` code at 300 ms. The first
bad commit was `1dee1c10219dd9a4f5c1898d8330ec42ef69260c`; its parent was
`d62908c9bea712613f0157102417f5249cbd73a7`.

Seven interleaved 1-second confirmation pairs measured the boundary:

| Scenario | `d62908c` | `1dee1c1` | Change |
| --- | ---: | ---: | ---: |
| warm `Local.Search` | 228.9 ns ±26% | 312.0 ns ±5% | **+36.30%**, p=0.001 |
| parallel Local | 239.6 ns/search ±42% | 322.7 ns/search ±8% | **+34.68%**, p=0.009 |

The broad confidence intervals include process-start outliers, but the
distributions remain separated and 15-second profiles reproduce 232.2 versus
312.2 ns/op. The parent profile has no chunk decoder. In `1dee1c1`,
`appendChunkValues` accounts for 17.51% flat / 27.40% cumulative CPU and
`runtime.memmove` for 9.88% flat. The commit replaced direct cached result
materialization, `append(result, values[id])`, with a generic slice append that
expands a chunk even when the experiment's shift is zero. This per-ID copy is
the conclusively identified additional work.

Comparable parallel-Local allocation profiles report no allocation in the
search path and one benchmark goroutine allocation per 100-search batch on
both revisions. The sampled profiles and unchanged alloc count show that the
small B/op difference is amortization over the slower revision's lower `b.N`,
not a new per-search allocation.

The experiment was never reachable from public build configuration: only
test-only build controls could select a nonzero shift. It was removed in full,
including runtime fields and branches, build controls, diagnostics, tests, and
benchmarks. The ordinary direct ID mapping now applies to `Search`,
`Local.Search`, and `Visit`.

Seven interleaved 1-second pairs compared committed baseline `a6cf049` with the
removal candidate:

| Scenario | `a6cf049` | removal | Change |
| --- | ---: | ---: | ---: |
| `Index.Search` | 30.67 µs ±5% | 30.63 µs ±6% | neutral, p=0.710 |
| warm `Local.Search` | 316.6 ns ±2% | 231.6 ns ±5% | **-26.85%**, p=0.001 |
| parallel Local | 328.8 ns/search ±2% | 242.6 ns/search ±2% | **-26.22%**, p=0.001 |

Allocation counts remain 28/0/1 respectively. A second 15-second CPU profile
measured 325.8 to 240.3 ns/op and contains neither `appendChunkValues` nor its
per-ID `memmove`. Interleaved 10-iteration Build and retained-memory runs were
neutral in latency, allocation count, retained index, and every Local class.

The final seven-pair release comparison measured:

| Scenario | `v0.8.2` | removal | Change |
| --- | ---: | ---: | ---: |
| `Index.Search` | 34.84 µs ±7% | 30.92 µs ±5% | **-11.27%**, p=0.001 |
| warm `Local.Search` | 232.3 ns ±3% | 235.3 ns ±3% | neutral, p=0.259 |
| parallel Local | 240.5 ns/search ±2% | 243.5 ns/search ±2% | neutral, p=0.058 |
| `Build` | 37.50 ms ±4% | 37.36 ms ±5% | neutral, p=0.710 |

Build has +0.34% transient bytes and +0.05% allocations relative to the
release. A `memprofilerate=1` differential profile localizes the 6.78 KiB
`buildIndexPhysicalAliases` delta mainly to `compileAllEqualityClasses`
(4.75 KiB) and streaming finalization (1.25 KiB), both post-release mechanisms
that support the retained 11.27% Index improvement. The removal itself is
resource-neutral relative to `a6cf049`.

## Environment and commands

- Apple M1 Max, macOS 26.6.2 arm64, Go 1.26.0;
- `GOMAXPROCS=1`, no race detector;
- detached worktrees for the release, candidate, and parent;
- `benchstat` from `golang.org/x/perf` revision `19be9d8e6c70`;
- release and candidate processes alternated in every pair.

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShape(Search|ParallelLocalBatch100|Build)$' \
  -benchmem -benchtime=500ms -count=1 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeRetainedMemory$' \
  -benchmem -benchtime=10x -count=1 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchmem -benchtime=3x -count=1 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShape(Search|ParallelLocalBatch100)$' \
  -benchmem -benchtime=1s -count=1 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' \
  -benchtime=15s -count=1 -cpuprofile=local.cpu .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeBuild$' -benchtime=1x -count=1 \
  -memprofile=build.mem -memprofilerate=1 .
```

Each command was invoked once per interleaved pair, for seven pairs. Both
revisions also passed `GOMAXPROCS=1 go test ./...`.

## Verification

The removal candidate passed `go test ./...`, `go test -race ./...` in 306.973
seconds, and `git diff --check`. Package statement coverage is 91.1%; targeted
working-tree diff coverage from `gocovdiff v1.4.2` is 96.7% (100% in
`builder.go` and `search_materialization.go`, 93.3% in `index_search.go`). All
changed files remain at or below 500 lines.

`make lint` reports the same 52 pre-existing findings on both clean `a6cf049`
and the removal candidate: 34 `lll`, eight `gocognit`, four `nestif`, three
`revive`, two `staticcheck`, and one `errcheck`. The removal introduces no new
lint finding; resolving this baseline backlog is outside this experiment
removal.

## Comparability and limitations

The current `production_shape_benchmark_test.go` was copied into the release
worktree so both revisions used identical fixture and loop code. Its common
exact search, parallel Local, build, and retained-memory cases compile and run
on both revisions. Cleanup calls execute outside the timed regions.

The current `BenchmarkProductionShapeLossySearch` cannot be compared with
`v0.8.2`: the release rejects the current full production Lossy schema with
`Lossy does not support this rule representation`. It is excluded rather than
compared against a different workload.
