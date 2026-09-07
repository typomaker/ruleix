# `v0.8.2` versus `HEAD` production benchmarks

Measured on 7 September 2026. The baseline is release tag `v0.8.2`
(`7f32ddc6ccf6b12419a1efb5af1424ff64353cc0`); the candidate is `HEAD`
(`66a7c643d9151cf04111a57038bc084304f8eb32`).

## Outcome

The candidate improves uncached `Index.Search` latency by 9.58%, but it is not
release-ready: warm `Local.Search` regresses by 38.07%, parallel Local latency
by 36.22%, and parallel transient bytes by 67 B per 100-search batch. Search
allocation counts do not change. The build is 2.62% slower, while its transient
bytes and allocation count change by only 0.13% and 0.05%.

The retained index grows by 0.17%. Cold, warm, adaptive, and adversarial Local
retained-memory classes are unchanged at benchmark resolution. The result is a
measured search regression under the project release policy; it is not accepted
as a performance trade-off.

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
```

Each command was invoked once per interleaved pair, for seven pairs. Both
revisions also passed `GOMAXPROCS=1 go test ./...`.

## Comparability and limitations

The current `production_shape_benchmark_test.go` was copied into the release
worktree so both revisions used identical fixture and loop code. Its common
exact search, parallel Local, build, and retained-memory cases compile and run
on both revisions. Cleanup calls execute outside the timed regions.

The current `BenchmarkProductionShapeLossySearch` cannot be compared with
`v0.8.2`: the release rejects the current full production Lossy schema with
`Lossy does not support this rule representation`. It is excluded rather than
compared against a different workload. The report does not profile or bisect
the accumulated Local regression; those are follow-up investigation work.
