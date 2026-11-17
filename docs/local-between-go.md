# Portable prepared Between bounds

## Selected implementation

The owner requested removal of the assembly implementation in 46d315f.
The selected replacement prepares one non-wildcard Between operand
for exact broad Local candidate sets of 9–16 IDs. It packs two uint16
bound pairs into each existing intersection scratch word. The scratch
owner is the compiled child ordinal. Every offer resolves current
inclusive endpoint positions and filters only the active candidate bits.

Other non-wildcard Between operands use their existing indexed bounds
directly and omit wildcard membership checks that cannot succeed.
Operands with any wildcard posting use the original scalar matcher.
The normal source-identity check and scratch reset handle source changes
and Local.Close. The narrow 1–4 and 5–8 paths use scalar matching and
the existing Include projections.

Production has one Go implementation for all architectures. The
candidate-mask representation uses its original int source ordinal
and original layout. The assembly, lane buffers, architecture-specific
dispatch, platform state and ordinal-compression guard from 46d315f
are removed. Existing API entry points and query semantics apply.

## Initial comparison and correction

Measurements use Apple M1 Max, macOS 26.6.2, Go 1.27.3,
GOMAXPROCS=1, the same 96,630-row dump and the same schema.
Each complete request uses one Local for 100 similar offers.
The 64-request generator yields 23 customers with 1–4 candidate IDs,
28 with 5–8 and 13 with 9–16. The baseline is db3ae77, before
assembly. Assembly control sources are frozen at 46d315f.

Simply replacing the cached NEON kernel with a Go active-bit loop
gave a small broad benefit and no mixed mean benefit. Two paired
50,000-request runs, ordered baseline, Go, assembly, assembly, Go,
baseline, measured the following complete-request means in µs.

| Cohort | db3ae77 | Initial Go | 46d315f assembly |
| --- | ---: | ---: | ---: |
| Mixed | 32.008 | 32.105 | 30.898 |
| 1–4 | 20.312 | 20.349 | 20.258 |
| 5–8 | 31.372 | 31.680 | 31.456 |
| 9–16 | 49.376 | 48.459 | 43.617 |
| Rotated | 36.521 | 36.123 | 35.247 |

That initial version sent all other operands through the ordinary
scalar fallback, which still tested empty wildcard bitmaps on failed
bounds. The correction keeps the ordinary wildcard matcher when
wildcards exist and performs direct bound comparisons when both
wildcard postings are empty. It shares rank resolution with the
cached operand and adds no per-offer buffer allocation.

The paired 100,000-request broad CPU profiles attribute 3.14 → 2.50 s
to the broad executor and 0.66 → 0.49 s to Bitmap.Contains. These
samples localize the removed membership work. CPU and allocation
profiles were collected for the initial Go, corrected Go, baseline
and assembly control.

## Corrected complete-request results

Three paired 80,000-request samples used order baseline, Go, Go,
baseline, baseline, Go. The table gives arithmetic means of per-run
means and per-run p99.9, in µs per complete 100-offer request.

| Cohort | Mean baseline → Go | p99.9 baseline → Go |
| --- | ---: | ---: |
| Mixed | 31.867 → 31.005 | 80.820 → 77.806 |
| 1–4 | 20.227 → 20.252 | 55.848 → 56.556 |
| 5–8 | 31.335 → 31.336 | 71.347 → 76.208 |
| 9–16 | 49.145 → 44.170 | 99.084 → 95.389 |
| Rotated | 36.271 → 35.425 | 86.570 → 96.500 |

Broad mean improved about 10.1% and mixed mean about 2.7%.
The corrected portable mean approaches the assembly control measured
in the first series, though the two series are separate comparisons.
The owner explicitly prioritized removing assembly over retaining
its last increment of speed.

Tail samples vary. Corrected rotated p99.9 samples were
123.416, 79.166 and 86.917 µs, compared with baseline
84.500, 86.292 and 88.917 µs. The narrow p99.9 ranges overlapped
and the scalar narrow means were close. These controls support the
broad and mixed mean benefit. They do not establish a consistent
tail improvement for narrow or rotated requests.

## Storage, Build and other controls

Two interleaved 8x Build controls measured roughly 150–151 ms and
about 46,373–46,374 allocations per index in both revisions.
Retained index heap remained around 2.585 MB.
Searched fresh Locals retained 3,427 B with 26 allocations.
Reopened contexts retained 3,431 B with about 14–15 allocations.

The main request preserved 12 allocations and 845 B. Narrow, medium
and broad cohorts preserved their 10/780, 11/819 and 19/1,015
allocation/byte classes. Two 50,000-request short-prefix samples
averaged 2.301 → 2.294 µs for one offer, 4.019 → 3.936 µs for four
and 7.813 → 7.674 µs for sixteen. Exact/lossy Index and Local
searches, joined queries, warm result hits and Close controls
preserved their result and allocation classes.

The full-dump Local-versus-Index differential check passed for baseline,
assembly, initial Go and corrected Go in grouped and rotated order.
Focused tests cover missing query values, inclusive bounds, inactive
bits, wildcard fallbacks, other scratch owners, absent bound entries
and the original candidate-mask size. Full package tests and vet passed.
Changed Go executable-line coverage is 55/55, or 100%.
Cross-builds passed for darwin/amd64 and linux/386.
The latter checks 32-bit int and packing portability. An initial
darwin/386 build was corrected to linux/386 because Go does not
support the darwin/386 OS/architecture pair.

## Reproduction

Set RULEIX_PRODUCTION_DUMP to the production JSON path. Both revisions
must use the current production_dump_workload_test.go so tiny and
medium selectors are available. Build baseline search sources from
db3ae77 and current search sources in separate test binaries.

Use GOMAXPROCS=1 and alternate binary order as recorded above.

```sh
go test -c -o ruleix.test .
./ruleix.test -test.run '^TestProductionDumpLocalMatchesIndex$'
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem
RULEIX_PLANNER_BUCKET=tiny ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem
RULEIX_PLANNER_BUCKET=medium ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem
RULEIX_PLANNER_BUCKET=broad ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem
RULEIX_PLANNER_ROTATE=1 ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem
```

Profiles add -test.cpuprofile=PATH and -test.memprofile=PATH at
100000x. Existing Build/retained probes use 8x, fresh/reopened probes
1000x and representative search gates 250ms.
The short/reopened helper is archived in
docs/experiments/local-neon/probe_test.go.txt and can be included
through a test overlay. It is independent of the assembly executor.
The historical assembly reproduction script freezes its selected
sources at 46d315f so current portable files cannot enter that comparison.
