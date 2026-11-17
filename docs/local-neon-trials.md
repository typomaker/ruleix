# ARM64 packed Between masks

## Selected path

The selected executor uses two ARM64 NEON kernels for prepared exact
Local candidate sets of 9–16 IDs. The first eligible Between operand
with empty wildcard postings packs its sixteen uint16 bounds into
the existing localAllIntersection.ids scratch once. Its child ordinal
identifies the scratch owner. Subsequent queries resolve current
endpoint ranks and reuse the packed immutable bounds. Other Between
operands gather candidate bounds and wildcard bytes into a stack
buffer and use the general kernel. Include operands use their existing
direct checks. Every result is intersected with the caller's active mask.

The ordinary narrow methods, including their original scalar
applyCandidateMask implementation and Include projection dispatch,
handle up to eight candidates. The broad vector interface is separate
from candidateMaskOperand. This avoids adding an additional method to
the interface asserted by the narrow executor. The normal
candidate-source identity check precedes every vector search.
Remembering a changed source and closing a Local clear the scratch
through the existing lifecycle. The candidate mask stores the scratch pointer in an architecture-specific
platform field. Its source ordinal uses uint32, matching the node-ID
domain, and an out-of-domain ordinal takes ordinary execution. On ARM64
this keeps the mask at its original 88 bytes. A size regression test also
covers generic targets. The generic platform field is empty. Remembering
a source establishes the scratch pointer and Close clears it with the
other mask fields. The shared search dispatcher keeps its original
five-argument broad helper call.

The ARM64 and generic implementations have separate build constraints.
The generic vector entry delegates to the ordinary scalar matcher.
The broad vector dispatcher is disabled by a compile-time constant on
other architectures and with the purego tag. Kernels consume only
pre-existing byte-sized endpoint dictionary ranks. They do not inspect
property names, customer fields, slot semantics, external IDs, or
domain-specific query values. The public API has no additional methods.

The general kernel compares sixteen lower and upper bytes, removes
missing concrete lower positions, applies independent wildcard lanes,
and extracts a sixteen-bit mask. The scratch kernel deinterleaves
sixteen packed uint16 pairs and performs the same non-wildcard checks.
The general upper coordinate is minUpper minus one, so absent queries
map to 255 and concrete upper bounds must be strictly greater than it.
Both kernels preserve the original inclusive boundary semantics.
All loads stay inside a 64-byte stack block or the 32-byte scratch.
The kernels use straight-line instructions without internal branches.

## Workload and decision criteria

Baseline search code is db3ae77. This is the same production executor
as 3e0fe2c. The experiment used the 96,630-row JSON dump with its
unchanged production schema and generated 64 customer requests.
Each request owns one Local and searches 100 offers. The existing
cohort selector was extended with tiny for 1–4 candidates and medium
for 5–8. There are 23 tiny, 28 medium and 13 broad requests.
The rotated control applies the existing stride-37 permutation.

Measurements used Apple M1 Max, macOS 26.6.2, Go 1.27.3 and
GOMAXPROCS=1 for timed processes. Tests and compilation used
GOMAXPROCS=2. The assembly has the same argument convention on the
supported ARM64 Go targets. Standard Go's ARM64 byte search and
equality routines also use these baseline SIMD instructions.
No optional crypto or newer ARM extension is required.

A prepared sixteen-candidate microbenchmark at 300ms and
GOMAXPROCS=2 measured 22.17 ns for the Go reference and 2.427 ns
for NEON. That benchmark excludes gather, rank resolution and Local
initialization. Complete-request results determine the selection.

## Preparation and fusion experiments

Two interleaved 40,000-request runs compared base, packed Go and
per-offer NEON, ordered base, Go, NEON, NEON, Go, base. Means in µs
were as follows.

| Cohort | Base | Packed Go | Per-offer NEON |
| --- | ---: | ---: | ---: |
| Mixed | 32.306 | 32.959 | 31.220 |
| 1–4 | 20.632 | 20.831 | 20.709 |
| 5–8 | 31.393 | 32.684 | 32.196 |
| 9–16 | 49.486 | 48.702 | 45.337 |
| Rotated | 36.546 | 36.602 | 35.850 |

Packing adds a buffer initialization, gathering and lane writes before
comparison. In a narrow search, Include filtering runs before Between
and can leave few active candidates. The scalar implementation visits
only those bits. The fixed-width vector work and packing did not
improve the narrow cohort in these samples. The broad 80,000-request
CPU profiles attributed 1.35 s to original Between mask evaluation,
1.27 s to packed Go and 1.06 s to per-offer NEON.

A fused kernel evaluated two Between predicates in one call with one
movemask. The first prototype passed stack-buffer pointers through
a generic interface. Escape analysis made these buffers heap objects.
Two paired 40,000-request samples changed 5–8 mean from 32.008 to
33.880 µs and p99.9 from 74.833 to 168.042 µs. Allocations rose from
11 to 30 per request. The broad mean changed from 50.245 to
52.252 µs, with p99.9 around 429 µs. CPU and allocation profiles
and the pointer-bearing interface call identify the added allocation
work.

Returning the packed structures by value removed the extra allocations.
In two interleaved 50,000-request samples, the full fused variant
measured 20.983 µs for 1–4, 33.066 µs for 5–8 and 49.202 µs for
9–16. Corresponding base means were 20.953, 31.779 and 50.011 µs.
The corrected medium profile attributed about 0.22 s to the fused
dispatcher and 0.17 s to preparation, including rank resolution and
gather. Assembly inspection shows the stack stores and copies for
returned lane arrays. The additional packing and dispatch did not
amortize for narrow masks. A broad-only by-value fused variant measured
49.096 µs for 9–16, slower than the single cached-predicate variant.

The single cached non-wildcard operand needs only 32 bytes, which fit
the existing intersection scratch. A two-sample 60,000-request series
measured broad mean 49.852 → 43.556 µs and p99.9
99.459 → 89.854 µs. Mixed mean changed 32.264 → 31.035 µs and
rotated mean 36.538 → 35.217 µs. Later trials isolated the broad
interface and restored the original narrow method names and bodies.


## Dispatcher and storage correction

Initial broad prototypes passed the scratch pointer as an extra argument.
The common searchAllMatches function retained its 992-byte frame for
the dump type, but disassembly showed changed register assignment and
an extra address calculation around the broad call. Small-cohort means
were about 1% higher in later interleaved controls. Moving the scratch
lookup into a pool-taking broad helper and restoring the scalar method
names did not remove the difference.

The final storage correction restores the original broad helper
signature and the original index_search.go source. A compact source
ordinal makes room for the scratch pointer within the mask's original
size. This limits the change in the common dispatcher's field access
to the count offset. The comparative register and CPU evidence
motivated the correction. Small timing changes beyond the emitted
work cannot be conclusively attributed to code layout from these
measurements alone.


## End-to-end validation

The final cold initialization establishes the scratch pointer only for
prepared broad sets containing a range operand. Small Locals avoid
the extra self-pointer write. Five paired 50,000-request controls before
this last cold guard measured small-cohort mean differences of
+0.71% and +0.46%, with overlapping samples. The final guard removes
work that those queries do not use.

The final three paired samples below used 50,000 complete requests
per cohort, ordered base, selected, selected, base, base, selected.
The table reports arithmetic means of the three run means and
run p99.9 values, in microseconds. These searches include initialization,
packing, result collection and Local.Close.

| Cohort | Mean base → selected | p99.9 base → selected |
| --- | ---: | ---: |
| 1–4 | 20.524 → 20.311 | 59.194 → 57.694 |
| 5–8 | 31.530 → 31.530 | 71.944 → 75.292 |
| 9–16 | 49.186 → 43.985 | 99.556 → 95.639 |
| Mixed | 31.991 → 30.971 | 81.708 → 78.875 |
| Rotated | 36.194 → 35.369 | 86.083 → 87.083 |

The broad mean improvement is about 10.6%, with a 3.9% lower average
sampled p99.9. Mixed mean improves 3.2% and sampled p99.9 improves 3.5%.
The narrow scalar controls establish no vectorization benefit.
Narrow and rotated tails did not move consistently in one direction.
For example, an earlier five-sample narrow control measured 5–8
p99.9 averages of 74.275 → 73.050 µs. The final three-sample control
above moved in the opposite direction. Final rotated p99.9 ranges
were 84.542–87.875 µs for base and 83.416–92.500 µs for selected.
These variations are retained in the report. They do not establish
a repeated regression or a dependable tail improvement for those cohorts.

The preceding three-pair 100,000-request confirmation with the same
kernels and owner layout, before the final narrow cold-write guard,
measured broad mean 49.012 → 43.577 µs and p99.9
98.291 → 90.570 µs. Mixed mean was 32.015 → 30.849 µs and
p99.9 84.597 → 77.681 µs. This is a separate series rather than
a pooled percentile. Stable mean improvements across the series are
stronger evidence than any single p99.9 sample.

## Storage and other search controls

The paired 8x Build probes stayed around 150–154 ms with about
46,373 allocations. Retained index probes stayed around 2.585 MB.
Fresh Local probes retained 3,427 B with 26 allocations.
Reopened Local probes retained about 3,431 B with 14–15 reported
allocations, overlapping the baseline's allocation class and timings.
The packed state occupies existing scratch and mask capacity.

The complete mixed request retained 12 allocations and 845 B.
The 1–4, 5–8 and 9–16 cohorts retained their respective 10/780,
11/819 and 19/1,015 allocation/byte classes. Short 1-, 4- and
16-offer probes, exact/lossy Index and Local controls, joined searches,
warm result hits and Close controls preserved their allocation classes.
All timing and footprint controls use the same dump and workload code
for base and selected search sources.

## Verification and reproduction

The baseline production sources are db3ae77. The selected sources
are in this commit. The archive under
docs/experiments/local-neon contains the nine intermediate source
variants, general and fused kernels, kernel tests, short/reopened
probes and an overlay preparation script. All nine intermediate
production variants reconstructed byte-for-byte from the archived
patches. Temporary binaries, profiles and the production dump are
kept outside the repository.

Native tests include 100,000 randomized general-kernel comparisons,
50,000 randomized scratch-kernel comparisons, independent scalar
Between checks with missing values, wildcards, inactive bits and
different scratch owners, source-change/Close checks, mask-size checks,
and an out-of-domain source-ordinal guard. Full-dump Local-versus-Index
checks cover 12,800 grouped/rotated offers for every prototype.
The by-value fusion correction also passed kernel and full-dump checks.

Native and purego package tests, go vet and an amd64 cross-build passed.
Combined changed Go executable-line coverage is recorded with the
final checks. Assembly is straight-line and independently exercised
against Go references. Go coverage does not count assembly instructions.

Run the archive from this commit with the production dump available.

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
sh docs/experiments/local-neon/run.sh
```

The runner retains its temporary directory. It prepares base and all
intermediate overlays against db3ae77 and selected against the checkout.
For the earliest gather comparisons use base, pack-go and pack-neon at
40000x in the order above. Narrow fusion comparisons use base, fused,
cached, then by-value corrections at 40000x or 50000x.
The selected confirmations used 100000x, 80000x and 50000x as recorded.
Set RULEIX_PLANNER_BUCKET to tiny, medium or broad. Set
RULEIX_PLANNER_ROTATE=1 for rotated mixed requests and clear the cohort
selector. A request always owns one Local.

Each complete-request invocation uses -test.run '^$',
-test.bench '^BenchmarkProductionDumpLocalRequest100$' and -test.benchmem.
CPU/allocation profiles add -test.cpuprofile=PATH and -test.memprofile=PATH.
The general microbenchmark uses -test.bench '^BenchmarkNEONKernel$'
with prepared sixteen-candidate data. Footprint controls use
BenchmarkProductionDump(Build|Retained)Probe at 8x and
BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe|ReopenedLocalRetainedProbe)
at 1000x. Short prefixes use BenchmarkSIMDShortRequests at 50000x.
Representative search gates use
BenchmarkProduction(JoinLocal|ShapeSearch|ShapeLossySearch|ShapeExactCacheEntryHit|ShapeLocalClose)
at 250ms. Compare modes interleaved, with GOMAXPROCS=1 and the same test sources.

The final native package tests and vet passed after the benchmark cohort
setup was refactored. Go diff coverage combines the native profile for
shared files with the purego profile for unchanged generic files:
94/95 changed executable lines, or 98.9%. The uncovered line is the
generic scratch getter returning nil, which the disabled generic vector
dispatcher does not call. Git whitespace checks and Ruff checks passed.
