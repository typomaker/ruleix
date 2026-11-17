# Incremental Between over a stable Local roster

## Decision

The event-by-event prototype was slower than the current executor.
Rebuild-on-change and cursor variants remain open experiments. Repeated
runs show a modest mean improvement from rebuilding, with overlapping
p99.9 samples and additional work on cursor misses. The final series
did not establish a repeatable material p99.9 gain suitable for adopting
a new search path. Checkpoint `587e917` served as the production baseline throughout.
The prototypes and reproduction harness are archived below.
This is an inconclusive tail-latency result for the simplified variants.

## Opportunity

Checkpoint `587e917` has the same executor as `3e0fe2c`. The 96,630-row
dump generated 510 unique-customer requests with a target of 512 seeds.
The 9–16 candidate cohort contains 122 requests of 100 offers. Each
request owns one Local. The stride-37 rotated control uses the same offers.

The probe resolves the leading posting as the executor does and counts
distinct non-wildcard endpoint ranks for its candidates. It records the
number of true candidate boundaries crossed per side, independent of the
number of bits that change in the final mask. A repeated interval crossing
is counted once, even when several candidates share its endpoint.
It distinguishes all adjacent offers from execution that reaches the
operand in the existing broad loop. The first offer initializes ordinary
search. All 12,078 subsequent offers reached both ranges. The two ranges
precede equality predicates in the compiled root on this dump.

| Reached transition count | No crossing | One crossing | Two crossings | Three or more |
| --- | ---: | ---: | ---: | ---: |
| Activity, both orders | 11,956 | 0 | 0 | 0 |
| Slot time, grouped | 3,371 | 3,146 | 1,793 | 3,646 |
| Slot time, rotated | 1,465 | 1,448 | 1,052 | 7,991 |

These rows compare successive offers that both reach the operand.
The slot-time no-crossing fraction is 28.2% grouped and 12.3% rotated.
Up to two crossings account for 69.5% grouped and 33.2% rotated.

## Implementations

All prototypes reuse the existing intersection scratch, storing a full
16-bit result mask and two eight-bit global endpoint coordinates in each
child slot. The scratch count byte marks ready entries. No new Local or
index allocation is needed. Existing source validation and scratch reset
apply when the roster changes. The path is gated by the existing prepared
exact root and only changes the 9–16 candidate executor.

The first prototype resolves current global ranks, compares them with the
previous ranks, and visits candidate bounds to identify endpoint crossings.
Only affected result bits are changed. The stable-coordinate path returns
the existing full mask intersected with the caller's active mask. A second
prototype retains that path and rebuilds the full mask when ranks change.
A third prototype checks the previous coordinate and advances at most two
positions before using binary search. Missing lower values map to zero,
missing upper values to 255, using the compact dictionary's existing
inclusive-bound semantics and wildcard membership.

## Profiles and correction attempts

The initial broad interleaved comparisons used two 60,000-request samples
per variant with order base, delta, rebuild, cursor, cursor, rebuild,
delta, base. The direct delta variant increased broad mean by about
12% and p99.9 by about 12% in that series. Its candidate loop
re-evaluated old and new endpoint membership for every ID, including
wildcard bitmap checks. The 100,000-request broad CPU profiles showed
Bitmap.Contains increasing from 0.58 to 0.78 s cumulative samples,
and the enclosing broad executor increasing from 2.79 to 3.22 s.
There was no allocation-class change.

Rebuilding the mask when positions change removes the old membership
checks. The cursor correction reduces rank-search work on stable or
nearby global coordinates, but a jump beyond the limit pays for the
unsuccessful probes and then binary search. The cursor profile attributed
1.00 s cumulative samples to rank resolution. These profiles include
ordinary benchmark initialization. The quoted functions are reached
during search. CPU samples are diagnostic, independently timed
interleaved requests determine the latency decision.

A subsequent three-sample series used 100,000 requests per mode and
order base, rebuild, cursor, cursor, rebuild, base, base, rebuild,
cursor. The ordinary narrow controls exposed overhead from adding
a broad-mode condition to the existing narrow operand method.
A separate private broad operand method was then tested to preserve
the narrow operand implementation byte-for-byte. This method still
uses the existing prepared-root eligibility and borrowed scratch.


## Repeated complete-request results

The final dispatcher-isolation series compared the checkpoint, rebuild
with a separate broad private method, and cursor with that method.
Each row summarizes three interleaved 80,000-request runs. Means are
averaged across runs. p99.9 is listed for every run in chronological
order, in microseconds. The order was base, rebuild, cursor, cursor,
rebuild, base, base, rebuild, cursor. The base used the same short-request
benchmark overlay as the prototypes.

| Version | Mode | Mean, µs | p99.9 samples, µs |
| --- | --- | ---: | --- |
| Base | Mixed | 32.218 | 84.834, 84.042, 131.875 |
| Rebuild | Mixed | 31.513 | 85.625, 82.959, 76.625 |
| Cursor | Mixed | 31.656 | 83.792, 81.291, 83.084 |
| Base | 9–16 | 49.068 | 99.291, 95.667, 102.167 |
| Rebuild | 9–16 | 46.540 | 102.167, 94.083, 104.041 |
| Cursor | 9–16 | 47.374 | 143.291, 95.333, 100.917 |
| Base | Rotated | 36.250 | 85.042, 83.417, 84.958 |
| Rebuild | Rotated | 35.806 | 87.041, 84.083, 80.917 |
| Cursor | Rotated | 36.367 | 86.875, 87.875, 85.667 |
| Base | Up to eight | 26.958 | 69.125, 69.666, 66.958 |
| Rebuild | Up to eight | 27.038 | 64.042, 65.875, 105.625 |
| Cursor | Up to eight | 26.993 | 70.291, 75.334, 66.458 |

The rebuild broad mean improved about 5.2%. Its broad p99.9 samples
overlap the checkpoint. The cursor miss path performs unsuccessful
neighbor comparisons before binary search. The three rotated cursor
means also exceeded all three checkpoint means in this series,
although the difference is small. Dispatcher isolation preserved the
existing narrow operand implementation. Narrow tail outliers and
complete-request tail outliers remain unexplained variability. They
are included above and cannot be attributed to the new design solely
from these latency samples.

Warm allocation classes stayed at 12 allocations and 845 B for the
mixed and rotated workloads, 19 and 1,015 B for broad, and 11 and
801 B for narrow. The two-pair initial layout gates retained 3,427 B
per fresh searched Local and 26 allocations. Retained index probes
were around 2.59 MB in all binaries and Build around 150–152 ms.
Representative Index/Local exact and lossy controls retained their
allocation classes and candidate counts.

Three short-request samples at 60,000 requests each measured means of
2.322, 4.103, and 7.868 µs for base prefixes of 1, 4, and 16 offers.
Rebuild measured 2.328, 4.084, and 7.757 µs. These are mixed-cohort
short requests, with setup shared outside each subbenchmark.

The final confirmation used three 200,000-request samples per mode,
with order base, rebuild, rebuild, base, base, rebuild.
The mixed means averaged 32.018 → 31.621 µs and rotated means
36.280 → 35.791 µs. Mixed p99.9 samples were
82.375, 81.625, 85.750 µs for base and
119.708, 79.084, 80.250 µs for rebuild. Rotated samples were
85.208, 85.667, 85.583 µs and
85.667, 81.084, 84.375 µs, respectively.
The mixed median improved slightly while the rebuild maximum was much
higher. This series also failed to establish a consistent tail benefit.

A final 100,000-request broad CPU/allocation profile pair with isolated
dispatch measured 48.944 → 46.637 µs mean and
96.959 → 98.333 µs p99.9. This pair reinforces the distinction between
a mean improvement and an inconclusive tail result.

## Reproduction

The census used Go 1.27.3 on Apple M1 Max, macOS 26.6.2,
darwin/arm64 and GOMAXPROCS=2. Timed test binaries used GOMAXPROCS=1.
Run from a checkout with the production code at `587e917`. The archive
under `docs/experiments/local-between-delta` contains the five exact
source variants, a differential test for missing values, wildcards,
endpoint inclusion, changing masks and scratch slots, and the measured
short/reopened benchmarks. The preparation script reproduced all five
production overlays byte-for-byte.

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
sh docs/experiments/local-between-delta/opportunity.sh
sh docs/experiments/local-between-delta/benchmark.sh
```

The timed runner retains its temporary directory. To reproduce the
initial full-request series, use the base, candidate, reuse, and cursor
binaries in the order described above and `-test.benchtime=60000x`.
The second series used 100,000 requests and the third 80,000.
Each timed invocation uses `-test.run '^$'`,
`-test.bench '^BenchmarkProductionDumpLocalRequest100$'` and
`-test.benchmem`. Set `RULEIX_PLANNER_BUCKET=broad` or `narrow` for
cohorts and `RULEIX_PLANNER_ROTATE=1` for rotation. Clear the other
selector when changing modes.

The broad profiles used `-test.benchtime=100000x`,
`-test.cpuprofile=PATH` and `-test.memprofile=PATH`. Inspect search work
with `go tool pprof -top -cum BINARY PATH`. Layout gates use
`BenchmarkProductionDump(Build|Retained)Probe` at 8x,
`BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe|ReopenedLocalRetainedProbe)`
at 1000x and
`BenchmarkProduction(JoinLocal|ShapeSearch|ShapeLossySearch|ShapeExactCacheEntryHit|ShapeLocalClose)`
at 250ms. The reopened probe warms and closes all contexts, then
reopens them for the measured next request and keeps them live through
the heap sample.


## Verification

All five prototypes passed the focused Between differential check and
the existing 12,800-offer grouped/rotating Local-versus-Index comparison.
The archived opportunity runner reproduced the histogram above.
All five archived production overlays matched the measured sources
byte-for-byte when reconstructed. The final dispatcher-isolated
differential checks also passed from the reconstructed overlays.

A final two-pair reopened-Local probe reported 3,431 B retained for the
checkpoint and 3,430–3,431 B for rebuild, with reported allocation
counts of 15 and 14–15, respectively. Timing overlapped around
35 µs per reopened request. These samples establish no retained-growth
concern for the proposed storage reuse and do not explain the tail
outliers.

Repository `go test ./...` with the dump enabled, `go vet ./...`,
`git diff --check` and Ruff checks for the archived Python scripts
passed. The commit contains the experiment archive and documentation.
