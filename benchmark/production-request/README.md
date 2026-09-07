# Production-shaped request benchmarks

This suite measures the matcher portion of a synthetic request workload. It
reuses the repository's 38,098-row production-shaped fixture, including its 17
fields, UUIDs, strings, numeric values, `time.Time` ranges, wildcards, and
custom `CompareBy` rules. Build, query generation, validation, and result-buffer
allocation are outside the timed section.

The production context motivating this benchmark is approximately 40K
constraints, 700 incoming requests/s, 10–100 lookups/request, and about 5 ms
p99.9 for the production matching phase. These observed figures are context,
not results from this synthetic suite. The suite asks how much of that cost can
be explained by the matching engine alone.

## Compared implementations

- `LinearNatural` is allocation-free plain Go in schema order.
- `LinearOptimized` checks selective UUID/platform dimensions first.
- `HandwrittenBitmap` directly indexes the most selective customer UUID with
  the same Roaring library as Ruleix and applies specialized residual checks.
- `RuleixIndex` and `RuleixLocal` use only Ruleix's public `Search` APIs. A
  distinct `Local` is created for each parallel worker.

OPA, GoRules/ZEN, and Grule are not in the direct table. None is already a
dependency, and adding one without an independently verified collect-all rule
model would risk comparing boolean/first-hit evaluation with Ruleix's set of
all matching IDs. They should be added only with the same 38,098 rows, all-ID
output, compilation outside timing, and the shared correctness test. Casbin is
also excluded because this range-heavy schema is not a natural policy model.

`TestProductionBenchmarkImplementationsProduceSameResults` compares sorted ID
sets for all 2,048 queries in Independent and Correlated modes, each with Hot
Context and Large Working Set. Independent queries vary all common dimensions.
Correlated blocks of 100 retain a customer/platform/store context while the
operation-specific date changes. Hot Context restricts common values to eight;
Large Working Set uses the full deterministic fixture cardinalities. No request
repeats one query 100 times.

## Reproduction

```sh
go test ./... -run '^TestProductionBenchmarkImplementationsProduceSameResults$'
go test -run '^$' -bench='^BenchmarkRequest/' -benchmem -count=10 . > /tmp/request.txt
benchstat /tmp/request.txt
go test -run '^$' -bench='^BenchmarkRequestParallel/' -benchmem -count=5 -cpu=1,2,4,8 .
go test -run '^$' -bench='^BenchmarkRequestScaling/' -benchmem .
go test -run '^$' -bench='^BenchmarkSyntheticMixedRequest/' -benchmem .
```

Each `BenchmarkRequest` operation is one request and reports `ns/op`, `B/op`,
`allocs/op`, `ns/lookup`, `lookups/s`, and `requests/s`. Variants contain 1,
10, 25, 50, or 100 sequential lookups. Scaling uses 10K, 38,098, 100K, and 1M
constraints for the exact implementations. `SyntheticMixedRequest` uses the
explicit, non-production-claimed mixture 10%×10, 30%×25, 30%×50, 20%×75,
10%×100 lookups.

The saturation/tail runner has no HTTP or serialization layer. It includes
queueing delay after synthetic arrival, uses one matcher per worker, and emits
CSV with achieved throughput, p50/p90/p99/p99.9/max, bytes, allocations, and GC:

```sh
RULEIX_LOAD=1 RULEIX_LOAD_DURATION=10s GOMAXPROCS=8 \
  RULEIX_LOAD_OUTPUT=/tmp/load.csv go test -run '^TestProductionRequestLoad$' -v .
```

`RULEIX_LOAD_RATES`, `RULEIX_LOAD_LOOKUPS`, and
`RULEIX_LOAD_IMPLEMENTATIONS` accept comma-separated overrides. The defaults
are 100/300/700/1000/2000 requests/s, 10/50/100 lookups, and the four exact
implementations. Overload drops arrivals when the bounded matcher queue is full;
compare `requests_completed` with `rate_target × duration`. CPU utilization is
not reported because portable Go runtime counters cannot isolate process CPU
time reproducibly; use an OS profiler alongside the runner when needed.

Generate charts from the CSV rather than embedding measurements:

```sh
python3 benchmark/production-request/plot.py /tmp/load.csv /tmp/p999.svg \
  --x lookups_per_request --y p999_us
```

## Latest local measurement

Environment: Apple M1 Max, macOS/arm64, Go 1.26.0, `GOMAXPROCS=1`, revision
`bafaee6` plus this benchmark change; 38,098 constraints; Independent/Large
Working Set; 300 ms × 5. Medians are filled from the reproducible run above,
not production telemetry.

| Implementation | µs/request (100) | µs/lookup | B/request | allocs/request | theoretical CPU cores at 700 req/s |
|---|---:|---:|---:|---:|---:|
| LinearNatural | 46,961 | 469.61 | 0 | 0 | 32.87 |
| LinearOptimized | 32,491 | 324.91 | 0 | 0 | 22.74 |
| HandwrittenBitmap | 8,005 | 80.05 | 22,400 | 200 | 5.60 |
| RuleixIndex | 4,453 | 44.53 | 4,279,684 | 2,855 | 3.12 |
| RuleixLocal | 6,986 | 69.86 | 5,129,527 | 1,924 | 4.89 |

The final column is only the theoretical CPU-time requirement:
`700 × seconds/request`. It is not capacity planning and excludes scheduling,
cache contention, GC interference, and application work. The 700 requests/s
target passes on one core only when measured request time is at most 1/700 s;
parallel results are the appropriate empirical target check.

In this large-working-set run every implementation misses 700 requests/s on a
single core at 100 lookups/request. The surprising `RuleixLocal` result is
workload-dependent: 2,048 rotating keys exceed its hottest cache behavior and
incur cache population; the Hot Context variants quantify the intended reuse
case separately. This result is retained rather than tuning the query stream to
favor the local cache.

Parallel smoke series on the same host (`200ms × 3`, medians, 100 lookups) gave:

| Implementation | 1 CPU | 2 CPU | 4 CPU | 8 CPU | first measured target pass |
|---|---:|---:|---:|---:|---|
| LinearNatural | 21 | 38 | 79 | 153 | none |
| LinearOptimized | 31 | 62 | 109 | 209 | none |
| HandwrittenBitmap | 120 | 238 | 460 | 892 | 8 CPU |
| RuleixIndex | 226 | 427 | 796 | 1,125 | 4 CPU |
| RuleixLocal | 170 | 341 | 663 | 1,162 | 8 CPU |

Values are requests/s. The short window is suitable for a checked-in local
checkpoint, not a deployment sizing decision; use the documented `count=5`
workflow and the saturation runner for a decision-quality rerun.

Short benchmark windows, synthetic deterministic distributions, shared-host
noise, and absent application work limit external validity. Percentiles require
the dedicated runner; `testing.B` aggregate timing must not be interpreted as
tail latency.
