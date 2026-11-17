# Local predicate projection and exact ID classes

The production baseline is `4de8383`. Revision `7896f53` adds only
documentation. The dump workload uses 96,630 rows, 1,447 internal IDs
and 64 customer requests with 100 offers each. Each request opens and
closes its own Local. The rotating order permutes the same offers
with stride 37.

## Opportunity measurements

The diagnostic follows the current admitted source when its exact
equality posting still matches the offer. Otherwise it runs current
ranking to sample the leading posting. Diagnostic ranking can update
the Local plan before the direct search. This is a workload
opportunity sample and has no role in the timed benchmarks.

Both orders produced 6,400 eligible sampled offers, including 5,100
with a source of up to eight IDs and 1,300 with nine to sixteen.
In each order 6,336 offers retained the preceding immutable source.
For each retained source the diagnostic tracked the exact Include
posting and the independent allowed endpoint coordinate for each
Between side. An absent coordinate used a separate presence key.

| Coordinate in compiled predicate order | Grouped previous | Rotating previous | Seen earlier under the source, either order |
| --- | ---: | ---: | ---: |
| Activity lower | 6,336 | 6,336 | 6,336 |
| Activity upper | 6,336 | 6,336 | 6,336 |
| Slot lower | 724 | 419 | 5,248 |
| Slot upper | 557 | 506 | 5,203 |
| Customer | 6,336 | 6,336 | 6,336 |
| Store | 5,163 | 0 | 5,163 |
| Slot type | 6,336 | 6,336 | 6,336 |
| Slot day | 4,479 | 2,285 | 6,208 |

The seen column counts offers that repeat an earlier coordinate during
the same contiguous source lifetime. It is a bound on opportunities
for a multi-entry cache. A single-entry cache depends on the preceding
coordinate and can have a lower hit rate when offers alternate.

## Exact ID equivalence

The diagnostic encoded every compiled operand for each internal ID.
For Include it enumerated the full immutable posting membership for
that ID and normalized wildcard membership to its matching behavior.
For each Between it encoded the two independent effective endpoint
positions and wildcard states using the compiled dictionary. These
compiled positions already include all repeated external ID rows
under the index's existing interval semantics.

The dump root had only these supported exact conjunctive operands
and an empty exclusion list. The resulting histogram contained
1,447 singleton classes. Exact per-operand equivalence would
preserve every ID on this fixture, so a Build-time compression
prototype has no measured work to remove here. Other root shapes,
exclusions, and lossy operators would require their own exact
signatures before enabling equivalence classes.

## First projection candidates

The first prototype maintains a bounded last-coordinate entry for
each operand of the verified source. Each entry tracks which slots
have been evaluated and which match. It fills only unknown active
slots, so the cost of a cache miss can still respect earlier
filters. Include uses immutable posting identity. Between caches
the inclusive lower and upper filters independently. The source
must resolve to the current exact posting on every use.

The first comparisons use the combined projection and isolate
equality-only and range-only projections. Short requests use
prefixes of one, four, and sixteen offers from the same generated
requests. These runs quantify setup cost and amortization.

### First paired measurements

Apple M1 Max, Go 1.27.3, `GOMAXPROCS=1`. Two interleaved
samples per binary used 45,000 grouped requests and 35,000 broad
and rotating requests. Short prefixes used 50,000 requests.

| Scenario | Baseline mean | Include and Between mean | Include only mean | Between only mean |
| --- | ---: | ---: | ---: | ---: |
| Grouped full dump | 35.67 µs | 28.06 µs | 28.48 µs | 37.00 µs |
| 9–16 IDs | 49.49 µs | 45.82 µs | 47.30 µs | 50.69 µs |
| Rotating | 36.35 µs | 36.57 µs | 37.13 µs | 38.19 µs |
| One offer | 2.28 µs | 2.38 µs | 2.37 µs | 2.30 µs |
| Four offers | 4.04 µs | 3.80 µs | 3.82 µs | 4.10 µs |
| Sixteen offers | 8.47 µs | 7.15 µs | 7.18 µs | 8.85 µs |

The grouped p99.9 of the combined candidate improved from 84.90 to
77.56 µs. Rotating p99.9 moved from 85.54 to 88.31 µs. Full
request allocations stayed at 12. The candidate retained 3,651 B
per fresh searched Local against 3,427 B on the baseline. Four
offers were the earliest sampled grouped prefix with a mean
benefit. The one-off prefix does not amortize preparation.

The next range prototype projects a leading posting into sorted
endpoint positions and cumulative masks at first use. Lookup
searches those local positions. The next Include prototype uses
exact posting ordinals and stores full masks in eight bounded slots.
It switches the existing partial-intersection scratch array into
projection mode for the verified source and resets on source
change or a return to intersection execution.

## Compact storage trials

The eager Between tables improved broad mean latency in the first pair
but added 224 retained bytes per Local. The packed Include variant
kept retained memory at 3,427 B fresh and 3,431 B after a seeded pool
was closed and reopened, matching the baseline in that control.
Its grouped narrow mean was 26.07 µs against 31.83 µs on the baseline.
The same cache across all mask sizes raised broad mean from about
51.23 to 51.91 µs and rotating mean from 36.82 to 38.41 µs. Tail
results for the eager candidate varied substantially between samples.

The next Include variant restricts projection to small candidate
sets and reserves one scratch slot per equality operand. A changed
key evaluates only the surviving candidates. A repeat prepares its
full mask, which later offers can reuse immediately. The broad mask
keeps its ordinary dispatch.

The repeat-admission variant reproduced the grouped advantage in
three interleaved samples. The mixed mean moved from 35.62 to 31.22 µs
and p99.9 from 91.88 to 82.83 µs. The narrow mean moved from 31.63 to
25.65 µs and p99.9 from 75.25 to 66.63 µs. Rotating mean was
36.39 → 36.67 µs and p99.9 was 85.54 → 89.08 µs. The two
Build and retained controls found 3,427 B per fresh Local on both
revisions and about 3,431 B per reopened context. The tested
shortest grouped prefix with a consistent mean benefit had
sixteen offers for this particular admission scheme.

A follow-up disables the projection for an operand that encounters
a different key before establishing a repeated entry. The range
follow-up packs the two independent endpoint coordinates into the
same scratch array and compares byte lanes against the query
coordinate in parallel. It only uses the broad mask path and
falls back for endpoints outside the packed coordinate range.

A positive-control equivalence diagnostic grouped two IDs with
identical compiled memberships assembled from differently paired
raw rows. An ID with fewer concrete memberships and a wildcard
ID each remained separate. This verifies that the fixture census
can detect equality despite different raw row order or pairing.

### Range controls and CPU profiles

The eager tables passed a focused comparison against independent
direct checks over inclusive, missing, and wildcard endpoints.
A bit-parallel variant packed one reached Between's endpoints into
the existing 32-byte intersection scratch. It passed the same
endpoint checks and a check of all 256 eight-slot bit patterns.
The 16-ID path combines two such words in original candidate order.

In three interleaved samples its grouped mean was 35.62 → 35.40 µs
and p99.9 was 84.88 → 81.04 µs. The broad mean moved 49.33 →
48.75 µs and p99.9 98.88 → 97.83 µs. The rotating p99.9 was
86.17 → 84.54 µs. The sample ranges overlap. Fresh retained memory
was 3,427 B, and reopened retention was approximately 3,431 B.
The packed version avoids the eager tables' 224 B retention cost.
The cost of computing query coordinates still appears on the hot
path. It projects one reached range per source without appending
another table to the Local state.

The 50,000-request rotating profiles found 0.52 s cumulative in
the baseline UUID equality mask and 0.38 s in the corresponding
packed equality calculation. On the candidate, the additional
dispatch wrapper accumulated 0.92 s across its predicates, and
runtime interface-table lookup accumulated 0.09 s. The total
rotating request still slowed. A follow-up routes narrow
projection through the existing mask operand interface and avoids
projecting an operand after consecutive unproductive keys.

In three interleaved bypass samples, the mixed mean moved from
35.52 to 32.56 µs and p99.9 from 82.58 to 83.88 µs. The narrow
mean and p99.9 moved 31.41 → 27.51 and 74.25 → 69.29 µs. The
broad path's mean was 49.17 → 49.21 µs. Rotating mean was
36.42 → 36.49 µs and p99.9 was 82.08 → 86.38 µs. The static
shape remained the same and the full-dump differential test
passed. Bypass removed most of the earlier rotating mean
penalty. The mixed tail and rotating tail still did not show
a paired improvement. The existing-mask-interface trial
removes the added projection type assertion.

### Retention and fallback checks

The compact range candidate used about 2.58 MB retained per
index in the same ten-Build controls as the baseline. Fresh
contexts allocated 3,829 B and 26 objects per request on both
revisions. After seeding, closing, and reopening 1,000 Locals,
the paired retained heap was about 3,431 B per context. This
sample holds the reopened contexts over two final garbage
collections. Measured allocation traffic in that sample can
vary with the number of pools recovered after close.

The verified exact and lossy Index and Local controls retained
their baseline allocation counts. The focused projection test
compared each cached and uncached equality result with the
ordinary mask for changing active slots and wildcard and missing
keys. It also verified a return to the regular intersection
scratch. The range test separately verified each endpoint
against direct matching.

## Selected projection

The selected implementation uses the prepared narrow mask executor's
existing operand interface. It reuses the partial intersection scratch
for up to eight independent exact Include slots. Each slot records
the operand and posting ordinal for its last key. A consecutive repeat
prepares the full mask. A second distinct key before a repeat disables
projection for that operand until the source changes. The next
intersection lookup switches the scratch back to its ordinary role.
The narrow source must resolve to the same immutable posting on each
offer before using any projected bits.

Three interleaved groups of 80,000 mixed and narrow requests per
binary give the first two rows. Three 40,000-request samples give
the broad and rotating rows.

| Scenario | Mean, baseline to candidate | p99.9, baseline to candidate | Allocations per request |
| --- | ---: | ---: | ---: |
| Full dump | 35.59 → 31.92 µs | 85.79 → 80.38 µs | 12 |
| Up to eight IDs | 31.37 → 27.05 µs | 74.50 → 70.33 µs | 11 |
| 9–16 IDs | 49.07 → 49.10 µs | 96.67 → 98.50 µs | 19 |
| Rotating | 36.31 → 36.26 µs | 85.13 → 83.83 µs | 12 |

The paired 60,000-request narrow profiles measured 31.45 →
26.94 µs mean and 74.21 → 65.92 µs p99.9. Prepared candidate
evaluation accumulated 0.94 → 0.81 s of CPU samples. Rotating
mean stayed near 36.4 µs in the profiles, with individual
profiled p99.9 samples of 85.71 and 91.08 µs. Rotating tails
overlapped in the interleaved runs. Short grouped prefixes first
showed a sampled mean benefit at sixteen offers. The one- and
four-offer means were close on both binaries.

Two paired Build and memory controls measured 150.82 → 149.92
ms and about 46,369 allocations per Build. Retained index heap
stayed around 2,582,572 B. Both retained 3,427 B per searched
fresh Local and about 3,431 B per reopened Local. Fresh and
reopened allocation counts were 26 and 15 in both versions,
respectively. The warm exact and lossy controls preserved their
allocation classes and the lossy candidate counts.

The range trials remain experiments. Eager tables have a
measurable retained cost and the packed range prototype's
mean benefit was small. The exact equivalence census provides
no matching classes larger than one ID on this dump.

## Benchmark setup

Use Go 1.27.3 on Apple M1 Max with `GOMAXPROCS=1` for timed
processes. Copy the same benchmark sources to both revision
checkouts and set `RULEIX_PRODUCTION_DUMP` to the dump path.
Alternate the test binaries. The full check uses the existing
dump request benchmark, the narrow and broad selections use
`RULEIX_PLANNER_BUCKET`, and the rotating check sets
`RULEIX_PLANNER_ROTATE=1`. For short trials, slice each generated
request to the desired prefix after validation and permutation
and use that length for per-offer reporting.

```bash
go test -c -o ruleix.test .
./ruleix.test -test.run '^TestProductionDumpLocalMatchesIndex$'
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchmem -test.benchtime=80000x
RULEIX_PLANNER_BUCKET=narrow ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchmem -test.benchtime=80000x
RULEIX_PLANNER_BUCKET=broad ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchmem -test.benchtime=40000x
RULEIX_PLANNER_ROTATE=1 ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchmem -test.benchtime=40000x
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDump(Build|Retained)Probe$' -test.benchmem -test.benchtime=10x
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe)$' -test.benchmem -test.benchtime=1000x
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProduction(JoinLocal|ShapeSearch|ShapeLossySearch|ShapeExactCacheEntryHit|ShapeLocalClose)$' -test.benchmem -test.benchtime=250ms
```

The reopened probe warms and closes all seeded contexts before
resetting the timer. It reopens the same number of contexts,
searches the next request for each, and holds the reopened
contexts through the final heap sample. CPU and allocation
profiles use the test binary's `-test.cpuprofile` and
`-test.memprofile` flags.

Final verification passed with the production dump enabled.
`go test -covermode=count -coverprofile selected.cover ./...`
covered all 60 changed executable lines. `go vet ./...` and
`git diff --check` passed.
