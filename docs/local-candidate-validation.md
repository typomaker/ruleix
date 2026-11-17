# Local candidate validation

The baseline was `7bd08d1`. Tests used the production dump of 96,630
conditions and 64 generated buyers, each with 100 offers per Local.
The temporary measurement overlay timed individual requests and
computed nearest-rank quantiles. The environment was Apple M1 Max,
macOS 26.6.2, Go 1.27.3, and `GOMAXPROCS=1`.

## Independent trials

Each binary had three interleaved samples of 25,000 requests.

| Trial | Mean | p99 | p99.9 | Allocations per request |
| --- | ---: | ---: | ---: | ---: |
| Baseline | 65.50 µs | 97.21 µs | 124.83 µs | 17 |
| Resolve reached Include postings once | 64.03 µs | 89.29 µs | 116.71 µs | 17 |
| Defer child cache lookups | 56.68 µs | 97.38 µs | 113.71 µs | 17 |
| Skip the already intersected operand | 65.30 µs | 96.13 µs | 118.88 µs | 17 |

All four used 921 B per request. The deferred-lookup trial mainly
benefited buyers with at most eight candidates. That group's p99 went
from 80.46 to 67.96 µs. Reusing Include postings mainly benefited buyers
with 9–16 candidates, whose p99 went from 106.79 to 96.75 µs.
The raw skip had a smaller effect and was also tested together with the
other changes, with an early return for empty intersections.

## Execution

Direct validation resolves Include postings lazily the first time a
candidate reaches them, when an unobserved Local validates at most
sixteen candidates and eight remaining operands. The resolved posting
and wildcard bitmap are used only within that offer. The getter and the
equality-key conversion are the same ones used by the original path.
An ordinary Index, an observed or instrumented search, and larger
validation groups use the existing per-ID path.

When the leading posting of a reused All plan is already available and
has at most eight candidates, later operands can be evaluated by direct
ID checks. The planner still looks for immutable postings and can end on
an exact empty result. It defers child cache lookups in unobserved
execution when every child supports direct validation and the learned
leading cardinality is also small.

## Combined runs

Three interleaved runs of 45,000 requests per binary gave these medians:

| Trial | Mean | p99 | p99.9 |
| --- | ---: | ---: | ---: |
| `7bd08d1` | 65.53 µs | 97.04 µs | 122.46 µs |
| Include resolution and deferred lookups | 55.27 µs | 88.88 µs | 108.71 µs |
| Also skip the intersected operand and return on empty intersection | 54.85 µs | 87.04 µs | 111.04 µs |

All three used 921 B and 17 allocations per request. The third change
had a smaller p99 effect and its p99.9 samples overlapped those of the
two-change candidate. It did not establish a tail improvement large
enough to keep the additional operand rearrangement.

Separate 50,000-request CPU and allocation profiles used buyers with
9–16 candidates and then buyers with at most eight candidates. CPU
samples in candidate collection for the first group fell from 1.15 to
0.74 s. In the second group CPU samples in All ranking fell from 1.08
to 0.70 s and mean request time went from 61.05 to 50.04 µs. Both
groups maintained their allocation counts and bytes per request.

The larger-candidate group had a higher p99.9 in the single profiled
sample, so it was repeated without profiling. Three interleaved
50,000-request samples per revision gave medians of 101.58 vs 93.50 µs
at p99 and 145.71 vs 130.00 µs at p99.9, with 43 allocations each.

Exact Index and Local, lossy search, warm exact-cache hits, exclusions
and Local teardown were exercised at 250 ms per sample. The apparent
short-run change in the cold joined batch did not recur in the
2,500-batch CPU and allocation profiles, which measured 1.210 and
1.208 ms per batch at 2,140 allocations. Separate 700 ms exclusion
and cold joined repeats were comparable. At 100 iterations per sample,
cold, warm, adaptive and adversarial Local retention stayed near
2,840, 94,064, 112,800 and 2,221,592 bytes per context.

Focused tests assert one Include getter call per reached posting,
including a missing or unmatched key with wildcard IDs. They also
verify that the saved-plan shortcut leaves observed bitmap lookups
available. The full suite and `go vet ./...` passed. Changed executable
line coverage was 59 of 61 lines.
