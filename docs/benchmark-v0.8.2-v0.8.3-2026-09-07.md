# `v0.8.2` versus `v0.8.3` production benchmarks

Measured on 7 September 2026. The baseline is `v0.8.2`
(`7f32ddc6ccf6b12419a1efb5af1424ff64353cc0`) and the candidate is `v0.8.3`
(`b9634a90549da35a0c3d6a523a6acc1a1c1891ec`).

## Outcome

`v0.8.3` improves production-shaped `Index.Search` latency by 10.59% with
unchanged search allocation classes. Warm `Local.Search` is 0.97% slower in
the interleaved confirmation series and 2.91% slower in separate 15-second CPU
profile runs. This release-relative Local regression is reproducible, but its
cause remains unresolved and it therefore stays under investigation.

Parallel Local is inconclusive: the confirmation series was 1.98% slower, but
the comparable 15-second profile runs both measured 250.4 ns/search. It is not
classified as a regression without a stable long-run reproduction. Build is
1.59% slower and uses 0.34% more transient bytes and 0.05% more allocations.
The retained index is 0.17% larger. Cold Local retained memory is unchanged;
the warm, adaptive, and adversarial Local classes are 0.24%, 0.22%, and 0.32%
smaller.

The release gate is not clean because the warm Local search regression has no
conclusive attribution or correction. No production change was made during
this measurement task.

## Comparable confirmation results

Seven baseline/candidate process pairs were alternated, with one second per
benchmark. Values are `benchstat` medians with 95% confidence intervals and
Mann-Whitney U-test results.

| Scenario | `v0.8.2` | `v0.8.3` | Change |
| --- | ---: | ---: | ---: |
| `Index.Search` | 33.75 us ±3% | 30.17 us ±3% | **-10.59%**, p=0.001 |
| warm `Local.Search` | 226.3 ns ±0% | 228.5 ns ±1% | **+0.97%**, p=0.001 |
| parallel Local | 237.7 ns/search ±3% | 242.4 ns/search ±2% | +1.98%, p=0.017 |
| `Build` | 37.10 ms ±0% | 37.69 ms ±1% | **+1.59%**, p=0.001 |

| Scenario | `v0.8.2` | `v0.8.3` | Change |
| --- | ---: | ---: | ---: |
| `Index.Search` | 40,805 B; 28 allocs | 40,805 B; 28 allocs | unchanged |
| warm `Local.Search` | 0 B; 0 allocs | 0 B; 0 allocs | unchanged |
| parallel Local batch | 108 B; 1 alloc | 110 B; 1 alloc | allocation count unchanged |
| `Build` | 4.806 MiB; 30,270 allocs | 4.822 MiB; 30,290 allocs | +0.34%; +0.05% |

The initial seven-pair 500 ms screen gave the same broad result: Index
-12.90%, warm Local neutral, parallel Local +2.31%, and Build +2.15%. The
one-second confirmation above is the primary comparison.

## Retained memory

Seven interleaved pairs used 10 indexes per retained-index sample and three
Locals per Local sample.

| Retained object | `v0.8.2` | `v0.8.3` | Change |
| --- | ---: | ---: | ---: |
| index | 1.260 MiB | 1.262 MiB | +0.17%, p=0.001 |
| cold Local | 2,968 B | 2,968 B | unchanged |
| warm Local | 92,432 B | 92,208 B | -0.24%, p=0.001 |
| adaptive Local | 111,056 B | 110,816 B | -0.22%, p=0.001 |
| adversarial Local | 74,272 B | 74,032 B | -0.32%, p=0.001 |

The retained-index build duration was neutral at 37.04 versus 37.37 ms
(`p=0.620`). Timing inside the Local retained-memory benchmarks is setup cost,
not a search gate.

## Regression investigation

Separate 15-second CPU-profile runs measured warm Local at 223.3 ns/op on
`v0.8.2` and 229.8 ns/op on `v0.8.3`, both at 0 B/op and 0 allocs/op. Parallel
Local measured 250.4 ns/search in both profiles. Allocation profiles show one
benchmark goroutine allocation per 100-search parallel batch on both releases
and no allocation in the Local search path.

The warm Local differential profile redistributes samples among
`loadLocalQueryResult`, equality query-key matching, and the surrounding
`searchAllMatches` call. It does not identify an additional operation that can
conclusively explain the small release-relative delta. In particular:

- the equality-unification boundary `a802264` to `cae0d03` was neutral at
  219.3 versus 220.1 ns/op (`p=0.598`, seven interleaved one-second pairs);
- the aggressive Local cache boundary `02f95f3` to `9a206c2` was neutral for
  Index, warm Local, and parallel Local (`p=0.535`, `0.805`, and `0.454`);
- `v0.8.3` and its immediate parent `c425a8c` have identical Go source,
  `go.mod`, and `go.sum`; the final commit changes only `LICENSE` and project
  governance documentation, so it cannot introduce executable work.

These checks reject the two initial attribution hypotheses but do not localize
the first causal change across the non-monotonic post-`v0.8.2` history. Project
policy does not allow the regression to be accepted or rejected on an
unresolved hypothesis. A correction task should continue with focused Local
microbenchmarks and controlled code-layout experiments before changing
production code.

## Environment and commands

- Apple M1 Max, macOS 26.6.2 arm64, Go 1.26.0;
- `GOMAXPROCS=1`, no race detector;
- detached worktrees for each revision;
- current `production_shape_benchmark_test.go` copied into the baseline and
  boundary worktrees so every revision used identical benchmark code;
- `benchstat` from `golang.org/x/perf` revision `19be9d8e6c70`.

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShape(Search|ParallelLocalBatch100|Build)$' \
  -benchmem -benchtime=1s -count=1 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeRetainedMemory$' \
  -benchmem -benchtime=10x -count=1 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchmem -benchtime=3x -count=1 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' \
  -benchmem -benchtime=15s -count=1 \
  -cpuprofile=local.cpu -memprofile=local.mem -memprofilerate=1 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeParallelLocalBatch100$' \
  -benchmem -benchtime=15s -count=1 \
  -cpuprofile=parallel.cpu -memprofile=parallel.mem -memprofilerate=1 .
```

Each comparison command was invoked once per interleaved pair. Both release
worktrees also passed `GOMAXPROCS=1 go test ./...`.

## Scope limitations

The production suite has no `Visit` benchmark, so this report makes no Visit
latency claim. The current production Lossy fixture cannot be compared with
`v0.8.2`, which rejects rule representations added after that release. As in
the earlier pre-release comparison, it is excluded instead of substituting a
different workload.
