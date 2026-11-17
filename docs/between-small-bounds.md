# Build-time Between endpoint dictionaries and range estimates

The baseline was `4e30e9f`. Experiments used the same dump, Apple M1 Max,
macOS 26.6.2, Go 1.27.3 and `GOMAXPROCS=1`. The dump has 23 and 37 distinct
activity endpoints, and 19 and 21 distinct slot endpoints. These indexes
fit in one block per side. They do not use the existing singleton
compaction layout, which requires at least eight blocks on each side.

An initial trial stored one byte per ID per side. It cut mixed-request
p99 from about 139 to 97 µs in alternating 40,000-request samples, while
increasing retained index memory by about 4.8 KB. The next trial encoded
the pair of endpoint positions in one dictionary entry and kept one
byte per ID. It also cloned append-grown posting storage before
interning, so the published index kept the same posting types with
smaller backing arrays. In alternating 30-build retained-memory samples,
the baseline retained approximately 2,577,083 B/index and the joint
dictionary trial retained 2,573,050 B/index.

The final implementation specializes direct ID validation under `All`.
It applies to single-block endpoint dictionaries that fit in byte
positions and have at most 255 nonzero pairs. The table
stores the earliest concrete lower and latest concrete upper bound for
each internal ID. It checks wildcard bounds separately. Large or small
sparse dictionaries use the existing matchers. The specialization
adds metadata only to eligible operands. Builds with a
declared Lossy memory limit keep their prior representation so the
new table stays outside that accounting contract.

## Final comparison

Three interleaved runs per binary timed 60,000 full requests each. Build
and retained-index probes alternated two 20-build samples per binary.
The build probe reused the builder and used the same decoded rows
for each iteration. The retained probe kept all built indexes live
while taking post-GC heap measurements.

| Metric | `4e30e9f` | Endpoint dictionary |
| --- | ---: | ---: |
| Mean request time | 75.85 µs | 65.49 µs |
| p99 request | 139.38 µs | 97.13 µs |
| p99.9 request | 162.71 µs | 121.33 µs |
| Request allocated bytes | 921 | 921 |
| Request allocations | 17 | 17 |
| Build time | 162.38 ms | 162.80 ms |
| Build allocated bytes | 4,431,663 | 4,491,480 |
| Build allocations | 45,681 | 46,452 |
| Retained bytes per dump index | 2,578,470 | 2,575,753 |

The 30-build allocation profile of the joint dictionary trial showed
the additional transient work in Roaring array cloning. In the final
70,000-request larger-buyer CPU profiles, direct range matching fell
from 1.27 to 0.19 sampled seconds. Request execution fell from 4.88 to
3.75 sampled seconds. Allocations were 1,390 B and 43 allocations per
request in both profiles.

Controls covered exact and lossy Index and Local search, warmed exact
cache entries, joined batches, and Local teardown. Three isolated
500 ms repeats measured 30.46 vs 30.26 µs for lossy Index search and
1.206 vs 1.205 ms for a cold joined Local batch, with unchanged
allocation counts. Two alternating 100-iteration retained-memory
runs per binary measured about 2,840, 94,064, 112,800, and 2,221,592 B
per Local in the cold, warm, adaptive, and adversarial cases.

The focused tests check duplicate IDs, wildcard bounds, exact
boundaries, dictionary overflow, and the Lossy memory-limit gate. The
full test suite and `go vet ./...` passed. Changed executable line
coverage was 87 of 89 lines.

## Within-block cardinality prefixes

Precomputing per-block prefix totals made the focused
`BenchmarkOrderedIndexCardinalityEstimate/BlockPrefix` about five times
faster, from 172 to 35 ns/op with no search allocations. However, the
three mixed 40,000-request samples remained about 76 µs on average
and 139 µs at p99, and the full-prefix variant added about 0.7 KB to
retained index memory. A packed one-block variant reused the block
prefix storage and kept the dump request timings comparable. It leaves
multi-block indexes on the original path. Broad CPU and allocation profiles for the full-prefix trial showed no
request-level gain. The packed variant kept the mixed request measurements
comparable. Neither prefix variant is part of the endpoint dictionary change.
