# Prepared early empty checks

## Scope and environment

Baseline: 5f6e63e, including the earlier [empty-plan correction](local-zero-plan.md).
The candidate is the accepted implementation in the commit containing this
report. Its production sources are the snapshot used for the selected timing
and profiling runs. The reproducer records resolved revisions and source hashes.

Measurements ran on Apple M1 Max, macOS 26.6.2, Go 1.27.3,
darwin/arm64, GOMAXPROCS=1, on 2026-11-17/18. Both builds retain
Roaring v2.26.0 and use identical schema, dump fixtures and benchmark loops.
The JSON dump has 96,630 rows and SHA-256
`ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.

The main workload selects 64 customers and executes 100 related offers through
one Local per request, then closes it. Customer parameters remain stable in the
main workload. Grouped offers change 1–3 store/slot fields. The 1–3, 5–8 and
9–16 cohorts contain 16, 28 and 13 customers; seven four-candidate customers
remain represented in mixed. Candidate count is the number of distinct
constraint UUIDs associated with the customer before other predicates.

Empty controls force an absent customer UUID, absent store UUID or absent slot
type while retaining a populated index. Absent keys were checked against the
dump. Existing-key-empty uses a present customer and a present, unrelated store;
all offers return no matches. Alternating switches between missing and present
customer values within a Local to test recovery and cached-source reuse. This
last control tests the generic API rather than the main one-customer scenario.
No production frequency for these controls is assumed.

This study uses a shared-index sub-benchmark matrix and a minimal shared test
build. Earlier absolute timings from the full repository test binary use a
different harness composition. Compare paired values within each study.

## Independent ideas and combinations

A learns the last rejecting root operand and checks its current query value
first. B prepares cheap-cardinality traversal order during Build. C prepares
exact equality key-absence checks for operators with no wildcard rows.
None of these mechanisms identify customer properties or use field names.

Initial independent controls used two runs per release, 40,000 requests per
cohort, in base/A/B/C/C/B/A/base order. The first A implementation reduced
missing-customer mean 12.442 → 6.370 µs, but alternating regressed
40.594 → 46.046 µs. CPU profiles found the added retry helper and the second
operand scan used to discover the failed child. The correction gates helper
entry and preserves the last admitted positive candidate source during a
missing-key offer. Its nil posting is an empty proof because admission already
proved immutable exact equality without wildcards. A different nonnil posting
still invalidates the source normally.

Merely remembering that a wildcard bitmap was empty did not produce a useful
standalone improvement: initial C mean was 12.787 µs against 12.442 µs.
The existing planning lookup already detects absent keys without materializing
a bitmap. C was therefore refined to prepare and invoke exact-key probes.

The following matrix screens all eight combinations. A here is the corrected
source-preserving variant, and C is the cold preflight variant. Two runs per
variant use 40,000 requests per cohort, traversing base/A/B/C/AB/AC/BC/ABC and
then reversing that order. Means are medians of the two per-run means, in µs.

| Variant | Missing customer | Missing store | Missing slot | Alternating | Mixed |
| --- | ---: | ---: | ---: | ---: | ---: |
| Base | 12.705 | 20.073 | 24.148 | 41.361 | 31.889 |
| A | 6.125 | 6.013 | 4.778 | 23.648 | 31.960 |
| B | 7.146 | 14.773 | 18.866 | 40.513 | 31.862 |
| C | 6.136 | 8.678 | 10.684 | 40.553 | 31.953 |
| AB | 6.067 | 6.012 | 4.723 | 23.454 | 31.804 |
| AC | 6.071 | 5.841 | 4.502 | 23.498 | 31.842 |
| BC | 6.457 | 8.754 | 10.681 | 40.950 | 31.934 |
| ABC | 6.138 | 5.795 | 4.495 | 23.439 | 31.919 |

Short controls exposed two additional costs. Discovering the rejecting operand
by rescanning slowed a one-off empty request from 308.9 to 370.4 ns.
Recording the child index at the existing zero check removes that second scan.
Cold preflight of every eligible key slowed one mixed offer from 2.193 to
2.284 µs. A matched 2,000,000-request CPU profile measured 2.327 → 2.420 µs
and attributed 0.13 s to the new preflight helper. It duplicates four key probes
before ordinary ranking on the first nonempty offer. The selected C check runs
only for an operand already known to reject, avoiding this speculative work.

Argument-passing and scoped-recording variants were also measured. Their raw
controls are archived, including periods with broad positive-path timing shifts.
Search entry frames remained 992 bytes in baseline and capture variants, so no
claim attributes those shifts to a larger entry frame. The accepted version
keeps the original ranking signature and records rejection through existing
scratch only during the initial root ranking. A generic retry at search entry
still added a condition to every offer. Moving the known-empty proof into the
existing cold result-lookup branch removed that entry condition. The final
controls below evaluate the resulting combined implementation independently.

## Accepted implementation

- Build encodes a stable cheap-first permutation for up to eight children in
  existing All padding. Identity order uses zero and follows ordinary traversal.
  Ranking writes to original child slots, preserving tie and result ordering.
- Build marks exact Include operands without wildcard rows or coarsening in an
  existing padding byte. Their key absence is a complete empty-result proof.
- During the initial root ranking, a transient minimum-int marker permits the
  existing zero checks to record the child index in candidate-mask scratch.
  Ranking estimates do not invoke nested child searches. The marker is cleared
  before materialization; nested groups cannot overwrite the root hint.
- While no All plan exists, result lookup rechecks a learned rejecting operand
  against the new query. A prepared exact-key check avoids wildcard and ranking
  work. Larger or unsupported groups use the generic cheap/planning fallback.
- A verified empty operand returns a read-only empty result through the existing
  result-assembly exit. The single static result retains no query data. The new hint
  stores only an operand index; it does not assume that any property is stable.
- A successful recheck clears the hint and resumes ordinary execution. After a
  plan exists, ordinary result caches and candidate-source checks handle later
  offers. A temporary absent source key preserves the prior valid source so
  subsequent present keys can reuse its IDs and prepared bounds.

Local.Close resets this scratch through the existing lifecycle. Observed,
counted and exclusion paths retain their existing execution behavior. The public
API is unchanged, and the implementation remains portable Go. No handwritten assembly,
field-role recognition, customer-specific branch or new per-query cache is added.

## Selected controls

Three 80,000-request runs per version alternate base, accepted, accepted, base,
base, accepted. Each table entry is the median of three per-run metrics, rather
than a pooled percentile. Times are µs per complete 100-offer request.

| Scenario | Mean base → accepted | p99 base → accepted | p99.9 base → accepted |
| --- | ---: | ---: | ---: |
| Missing customer | 12.411 → 6.323 | 13.750 → 6.958 | 28.917 → 15.917 |
| Missing store | 20.133 → 6.037 | 23.500 → 6.625 | 40.208 → 15.791 |
| Missing slot type | 24.325 → 4.670 | 28.292 → 5.125 | 47.917 → 11.291 |
| Existing-key-empty | 19.701 → 19.655 | 45.084 → 44.166 | 65.375 → 58.417 |
| Alternating | 41.005 → 23.498 | 64.250 → 36.125 | 93.417 → 56.500 |
| Mixed | 31.889 → 31.956 | 57.167 → 57.167 | 75.666 → 74.542 |
| 1–3 | 19.602 → 19.433 | 27.750 → 26.125 | 53.750 → 49.041 |
| 5–8 | 31.948 → 32.100 | 45.375 → 46.000 | 67.333 → 66.959 |
| 9–16 | 46.679 → 46.703 | 60.959 → 60.917 | 89.667 → 85.834 |
| Rotating | 36.122 → 36.130 | 58.083 → 57.750 | 78.458 → 76.541 |

Missing-customer, missing-store and missing-slot means improve 1.96x, 3.33x and
5.21x. The existing-key-empty scenario retains its earlier speed: individual
operands are nonempty, so a remembered single-operand rejection cannot prove
this intersection empty. The ordinary mask/intersection exit remains active.

Allocation traffic is unchanged for all scenarios except alternating, which
falls from 864 B / 14 allocations to 830 B / 12 allocations per request.
The three missing-key controls retain 24 B / 1 allocation. Mixed retains
845 B / 12 allocations; 1–3 retains 776 B / 10; 5–8 retains 819 B / 11;
9–16 retains 1,015 B / 19; rotating retains 845 B / 12. Existing-key-empty
retains 786 B / 10 allocations.

The same three paired runs also exercise public Search paths and short requests.
Index and warm-Local rows are per 100 searches; prefix rows are per request.

| Control | Mean base → accepted | Bytes / allocations, both |
| --- | ---: | ---: |
| Index | 105.585 → 106.046 µs | 6,207 / 341 |
| Warm Local | 30.471 → 30.501 µs | 6 / 0 |
| Missing customer, 1 offer | 307.5 → 256.7 ns | 24 / 1 |
| Missing customer, 4 offers | 679.1 → 445.8 ns | 24 / 1 |
| Missing customer, 16 offers | 2.119 → 1.162 µs | 24 / 1 |
| Mixed, 1 offer | 2.239 → 2.242 µs | 832 / 12 |
| Mixed, 4 offers | 4.002 → 4.024 µs | 835 / 12 |
| Mixed, 16 offers | 7.781 → 7.757 µs | 836 / 12 |

Nonempty means, including the public and short controls, remain within about
0.6%. Raw samples are retained so this small spread is not presented as a
separate positive-path improvement.

## Build, memory and profiles

Two storage runs per version alternate base, accepted, accepted, base. Build and
index probes use 10x; fresh/reopened probes use 1000x. Values average both runs.

| Metric | Base | Accepted |
| --- | ---: | ---: |
| Build mean | 155.25 ms | 150.57 ms |
| Build transient bytes | 4,784,984 | 4,782,782 |
| Build allocations | 46,369.5 | 46,369 |
| Retained index bytes | 2,581,432.5 | 2,582,534.5 |
| Fresh Local bytes allocated / allocations | 3,829 / 26 | 3,829 / 26 |
| Fresh Local bytes retained | 3,427 | 3,427 |
| Reopened Local bytes retained | 3,431 | 3,431 |

Index footprint differs by 0.043%; fresh and reopened retained Local values
match. On the measured 64-bit target, All remains 160 bytes, equality 152,
bitmapPool 248, plain Local pool 336 and candidate mask 88. Metadata fits existing
padding, while the learned index uses existing scratch. The static empty result
is shared program data rather than per-Index or per-Local heap state.

Comparable CPU profiles use 250,000 requests per scenario for baseline and
accepted, covering missing-customer, mixed and alternating. Profiles also
include fixture setup and sample sorting, so raw profile totals do not represent
request latency. For a stable missing key, only the first of 100 offers reaches
ranking; the remaining offers recheck that operand and return a verified empty
result before ranking or materialization.

Allocation profiles use 10,000 alternating requests with memprofilerate=1 and
focus on searchProductionDumpRequest. Preserving the valid source avoids
rebuilding temporary Roaring containers after a missing key. In the earlier
source-preserving control, array-container allocations fell 21,863 → 11,250,
small array growth allocations 19,207 → 14,212 and clones 6,091 → 5,779:
15,920 fewer objects for 10,000 requests. Result-output allocation counts remain
conserved across their shifted assembly sites. The final focused profiles are
archived as well. Profile-instrumented timings are excluded from latency tables.

## Verification and reproduction

The final dump test passed 32,000 offers across five additional empty/recovery
controls, with expected-empty assertions and ordered result comparisons against
Index.Search. The original 12,800-offer differential and result digest also pass.
Both versions retain SHA-256
`0ef4f277cac13b64449e69d6880f0b5a8a7d421670438ec141a20667d9784376`,
including 1,248 matches in 128 grouped/rotated requests.

Full tests passed with 92.0% package statement coverage. Changed executable
statement-start lines have 130/138 coverage (94.2%); the broader changed
coverage-block line count is 161/173 (93.1%). Both exceed the 90% requirement.
Tests cover changing rejection fields, optional values, larger groups, source
preservation, prefix preservation, independent Locals and Close/reopen. Changed
code lint passed using golangci-lint v2.12.2 with Go 1.26.9 because that linter
cannot decode the installed Go 1.27 export format. The baseline's full-lint
findings are recorded in the preceding study; no new lint issues were added.

Focused race tests passed, including eight concurrent Locals repeatedly
transitioning between missing and present keys and closing/reopening contexts.
The exact candidate compiled for native darwin/arm64, linux/amd64 and linux/386.
Foreign-target checks verify compilation; timing and runtime tests use M1 Max.

Go formatting, shell syntax, Python lint and Git whitespace checks passed.
The coverage helper in this directory reads Git changes against 5f6e63e and
counts executable statement-start lines, excluding comments and structural
braces. All changed repository files remain within 500 lines.

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
sh docs/experiments/early-empty/run.sh > /tmp/early-empty.log 2>&1
python3 docs/experiments/early-empty/matrix-summary.py /tmp/early-empty.log
```

The runner archives 5f6e63e, copies identical shared fixtures and overlays the
candidate sources from HEAD. It records source hashes, checks correctness and
alternates timing/storage runs. Set RULEIX_EMPTY_CANDIDATE to an exact revision
or WORKTREE for uncommitted work. Python 3.12+, Go and rg are required. Overlay
paths are canonicalized and byte-identical candidate/baseline binaries fail
validation, preventing silent ignored-overlay measurements.

To repeat the independent matrix, set RULEIX_EMPTY_VARIANTS to
`base a2 b c2 ab ac bc abc`, RULEIX_EMPTY_SEQUENCE to that sequence followed
by its reverse, and RULEIX_EMPTY_ITERATIONS=40000x. The archived generators also retain
the initial and capture/scoping iterations. The default runner measures only
baseline and accepted. [Records and profile summaries](experiments/early-empty/records/)
include every reported phase, including superseded entry controls.
