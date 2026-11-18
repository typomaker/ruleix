# Experimental batch search

## Contract and status

`Index.SearchBatch(conditions []T, found [][]ID)` appends each condition's
matches to `found[i]`, preserving first-insertion ID order and existing results.
Outer lengths must agree; a mismatch panics before any result changes. Empty
batches do nothing. Inner destinations must have independent backing storage
and must not overlap input storage. Getters and comparators must be stable;
callers must not concurrently mutate inputs or destinations.

The Index remains concurrently shareable with independent destinations. Both
existing Search methods remain available. This is an experimental additional
API, not a migration of Index.Search or Local.Search. Test-only strategy
methods are absent from ordinary builds. The correctness and production
performance reference is one Local per request and a loop of Local.Search.

## Selected execution

The planner reuses the execution descriptors and physical representations
already prepared by Build. There is no reflection, customer-field recognition,
new Index field, new Local field or batch-specific Build traversal. A rule's
own optional getter values and comparator semantics define its query keys.

1. Validate outer lengths. The call owns a stack-allocated Local handle using
   the same pool acquisition, observation and Close protocol as Index.Local.
   Calls with fewer than 16 conditions use it directly, avoiding batch analysis. The owned context and destination pointers
   are valid, so this path invokes the same internal search core without
   repeating public Local.Search's open-context and nil-pointer checks.
2. Require Build's existing native-mask capability for the All root. This
   currently covers groups of two to eight equality/compiled-Between operands;
   other shapes retain Local execution before batch analysis. Choose a selective
   exact equality operand without wildcard rows. A missing key is a complete empty-result proof. A leading
   posting wider than 64 candidates takes the existing Local execution path.
3. Resolve anchor postings over at most 128 offers at a time. Repeated exact
   values reuse one lookup. Group actual offers by immutable posting identity;
   missing postings drop only their associated offers. Each subgroup preserves
   its original destination index. Wide later subgroups use the same Local.
4. Before decoding more than 16 candidates for a subgroup of at most four
   offers, test whether every offer has an exact global empty proof. This
   avoids wide mask preparation for very short out-of-range calls. Otherwise
   maintain a uint64 candidate mask for each offer. Process equality operands
   before range operands and stop once every mask in a subgroup is empty.
5. Equality uses immutable concrete-posting identity plus its wildcard mask.
   Compiled Between uses exact boundary-position pairs from the Build dictionary,
   including presence and wildcard semantics. Equal physical result keys reuse
   one mask. This computes shared subgroup intersections without constructing
   speculative combinations or caching every unique query value.
6. Unsupported roots, ordinary ordered/uncompiled-Between matchers, absent safe
   anchors and sampled observed contexts retain Local execution. The generic
   experiment controls can still exercise direct matchers independently.
   Apply exclusions per surviving ID and append each offer's ordered results.

The 64-candidate bound is the native mask capacity. The 128-offer chunk bounds
analysis storage and contains the supplied 100-offer request; larger calls are
processed in chunks without changing result association. These are capacity
limits, not inferred customer semantics. Chunking does not release the owned
Local, so a call still has at most one Local context.

## Alternatives and diagnosis

All alternatives remain internal experiment controls and are selectable only
through test methods. They are not supported public policy flags.

| Control | Analysis and execution |
| --- | --- |
| Local | One Local and independent public Local.Search calls |
| Common | Share common operands; evaluate varying operands per offer |
| Full | Classify every operand before any candidate pruning |
| Staged | Classify and cache only the currently surviving offers per operand |
| Lazy | Offer-first execution with a last semantic key/result per operand |
| Subgroups | Staged execution inside actual anchor-posting subgroups |
| Candidates | Candidate-first direct checks broadcast to semantic offer classes |
| Typed / Auto | Staged subgroups using posting and compiled-boundary identities |

Initial full/lazy analysis allocated a fixed plan for every operand. Allocation
profiles identified that plan slab and boxed Between keys; CPU profiles showed
repeated key comparisons and getter/comparator work. Mitigations recycle and
clear Full plans, bound Lazy to a last-key entry, order Candidates cheap-first,
and avoid redundant Common mask evaluations. Full analysis still resolves
values for offers that earlier operands would reject. Typed execution avoids
boxed query keys and compares exact physical result identities instead.

The first Typed version passed local arrays through an interface, allocating
rows, masks and candidate storage once per subgroup. The allocation profile
attributed the dominant unrelated-batch allocation to searchBatchChunk. Recycling
representation-only scratch removed that per-subgroup allocation. All posting
references are cleared before recycling; no input values or originating Index
are retained by the scratch pool. Concurrent calls borrow separate scratch.

The stack-owned handle removes the allocation of a returned public Local
handle without changing the public Local implementation or its lifecycle.
Cold pool/state acquisition and caller output growth can still allocate.

Short controls exposed anchor-selection overhead: a two-offer missing key
and a four-offer wide fallback could cost more than the Local loop. The final
conservative dispatch floor is 16 offers, where the measured native shapes
amortize preparation. This leaves some possible wins for three/four positive
offers unused. Short subgroups inside a larger batch still use native masks
and exact empty proofs. The floor is a workload-derived policy, not a universal
optimality claim.

The synthetic 32-candidate case exposed a separate capability cliff: that
Build used ordinary Between matchers, so the first dispatcher repeated ordered
index walks per ID while Local materialized/cached unique ranges. Focused CPU
profiles attributed 87.6% of sampled batch execution to ordered matchesID.
The dispatcher now requires Build's existing native-mask capability before
selecting the batch engine. This preserves Local execution for the ordinary
representation while retaining the compiled 64-candidate specialization.

Forward/reverse screens use two samples per strategy; values are median means in µs.

| Strategy | Mixed | Rotated | Unrelated | Mixed B/op | Mixed allocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| Local | 31.640 | 35.614 | 173.824 | 809 | 11 |
| Common | 42.280 | 58.761 | 175.742 | 1375 | 35 |
| Full | 103.961 | 118.448 | 179.798 | 2528 | 53 |
| Staged | 27.353 | 42.618 | 176.907 | 1375 | 35 |
| Lazy | 28.700 | 46.998 | 176.541 | 1717 | 42 |
| Subgroups | 26.934 | 42.605 | 80.147 | 1375 | 35 |
| Candidates | 33.725 | 53.367 | 120.420 | 1375 | 35 |
| Typed | 12.486 | 14.284 | 60.924 | 3 | 0 |

The final experiment bridge uses a switch, not a freshly constructed strategy map; strategy-selection allocations are absent from these comparisons. Typed and Auto execute the same policy.

## Comparable production measurements

The baseline release is v0.9.0, d47c5a0ce9ec9ad22e26cc1962b506bdf9029eb0.
The candidate adds four batch source files to that production tree;
existing production source and dependencies are unchanged. Exact file hashes,
shared fixtures and raw logs are archived in
[the experiment bundle](experiments/search-batch/README.md).

Environment: Apple M1 Max, darwin/arm64, macOS 26.6.2, Go 1.27.3,
GOMAXPROCS=1. The supplied schema and 96,630 dump rows are unchanged; dump SHA-256
is `ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.
The main generator supplies 64 customer requests. Tiny/medium/broad cohorts
contain 16/28/13 customers and mean 1–3/5–8/9–16 initial candidates respectively.
A primary request contains 100 offers; the source generator changes 1–3
store/slot properties between adjacent offers while customer properties stay
fixed. Rotated offers deliberately disturb adjacency. Partial and unrelated
controls deliberately mix four or many customers and are separate stress cases.

Each strategy receives the same independent per-offer output buffers, reused
and length-reset between requests. The entire call is timed, including analysis,
Local lifecycle, exclusions and appends. Go ns/op also includes the common
output-length reset, clock and sample bookkeeping; percentile samples surround
the complete search helper. Thus these values should not be
compared directly with older studies that reused one temporary output buffer
across all offers. Index.Search is not a production timing baseline here.

Final primary runs interleave Local/Auto/Auto/Local/Local/Auto, 40,000 requests per
cohort per run. Tables use medians of each run's metrics; percentiles are not
pooled. Short controls use the same order at 20,000 operations. Alternative
screens run forward and reverse strategy orders at 3,000 operations; their
small-sample tails are not acceptance evidence. Compatibility controls compare
released Local with candidate Local in old/new/new/old order at 20,000 requests.

All times are µs per complete 100-offer request. Byte/allocation values are per request.

| Case | Mean Local → Batch | p99 Local → Batch | p99.9 Local → Batch | B/op Local → Batch | Allocations Local → Batch |
| --- | ---: | ---: | ---: | ---: | ---: |
| Mixed | 31.841 → 12.385 | 56.125 → 22.959 | 69.792 → 29.709 | 807 → 0 | 11 → 0 |
| Missing customer | 6.158 → 1.493 | 6.667 → 1.541 | 13.625 → 1.708 | 24 → 0 | 1 → 0 |
| Missing store | 6.028 → 1.553 | 6.542 → 1.625 | 14.084 → 2.041 | 24 → 0 | 1 → 0 |
| Missing slot type | 4.696 → 1.422 | 5.083 → 1.459 | 11.792 → 1.625 | 24 → 0 | 1 → 0 |
| Existing keys, empty intersection | 19.174 → 4.268 | 43.500 → 4.667 | 59.792 → 13.583 | 786 → 0 | 10 → 0 |
| Alternating missing/present customer | 23.414 → 10.768 | 35.500 → 19.209 | 55.833 → 25.375 | 794 → 0 | 10 → 0 |
| 1–3 customer candidates | 19.090 → 7.163 | 24.917 → 9.250 | 46.000 → 20.917 | 760 → 0 | 9 → 0 |
| 5–8 customer candidates | 32.141 → 13.374 | 46.333 → 16.958 | 65.666 → 28.500 | 776 → 0 | 10 → 0 |
| 9–16 customer candidates | 46.263 → 15.898 | 59.041 → 22.541 | 81.417 → 34.125 | 957 → 0 | 17 → 0 |
| Rotated offers | 36.165 → 14.164 | 57.125 → 25.375 | 79.625 → 34.000 | 807 → 0 | 11 → 0 |
| Identical offers | 39.719 → 16.993 | 60.750 → 20.709 | 77.667 → 35.792 | 807 → 0 | 11 → 0 |
| Unrelated offers | 174.262 → 60.939 | 249.916 → 76.959 | 345.375 → 103.042 | 17125 → 0 | 302 → 0 |
| Four-customer partial groups | 114.505 → 25.513 | 161.166 → 36.625 | 189.708 → 57.708 | 1171 → 0 | 30 → 0 |

Short mean times are µs per whole call; all short results remain in the archived summaries. Instrumentation contributes a shared clock/sampling floor, especially for empty calls.

| Case | Offers | Mean Local → Batch | Allocations Local → Batch |
| --- | ---: | ---: | ---: |
| Mixed | 0 | 0.229 → 0.059 | 1 → 0 |
| Mixed | 1 | 2.124 → 2.120 | 11 → 10 |
| Mixed | 2 | 2.726 → 2.740 | 11 → 10 |
| Mixed | 3 | 3.512 → 3.486 | 11 → 10 |
| Mixed | 4 | 3.892 → 3.859 | 11 → 10 |
| Mixed | 16 | 7.748 → 4.154 | 11 → 0 |
| Missing customer | 0 | 0.229 → 0.059 | 1 → 0 |
| Missing customer | 1 | 0.301 → 0.294 | 1 → 0 |
| Missing customer | 2 | 0.362 → 0.355 | 1 → 0 |
| Missing customer | 3 | 0.426 → 0.410 | 1 → 0 |
| Missing customer | 4 | 0.492 → 0.473 | 1 → 0 |
| Missing customer | 16 | 1.214 → 0.506 | 1 → 0 |
| Unrelated offers | 0 | 0.228 → 0.058 | 1 → 0 |
| Unrelated offers | 1 | 2.113 → 2.097 | 11 → 10 |
| Unrelated offers | 2 | 3.876 → 3.826 | 14 → 13 |
| Unrelated offers | 3 | 6.102 → 5.975 | 19 → 18 |
| Unrelated offers | 4 | 8.118 → 8.218 | 25 → 24 |
| Unrelated offers | 16 | 32.794 → 10.938 | 85 → 0 |

The unchanged public Local implementation is separately checked against the release:

| Case | Release Local mean µs | Candidate Local mean µs |
| --- | ---: | ---: |
| Mixed | 31.777 | 31.649 |
| Missing customer | 6.194 | 6.236 |
| Unrelated offers | 175.365 | 174.351 |

Release/candidate Local allocation metrics agree. These two-sample controls check compatibility, not a new release tail claim.

## Synthetic capability controls

Interleaved Local/Auto/Auto/Local shape screens use 2,000 operations per
candidate-count/offer-count case and verify results before timing. The final
32-candidate ordinary-Between fallback takes 34.810 → 34.758 µs for 100 offers,
replacing the original 359.042 µs repeated-ID path (Local 42.465 µs under
profiling). Compiled 64-candidate execution takes 42.276 → 31.389 µs.
Four empty offers with 64 candidates use the short path: 0.626 → 0.603 µs;
the formerly exposed wide fallback at 4096 candidates takes 0.610 → 0.600 µs.

Two short fallback outliers in the initial two-sample screen did not reproduce
in focused Local/Auto/Auto/Local/Local/Auto confirmation at 40,000 operations:
32-candidate four/sixteen-offer medians were 5.654/20.988 → 5.613/20.782 µs.
The [raw confirmation](experiments/search-batch/fallback-verify-1-Local.txt)
and remaining runs are archived. These controls separate a verified capability
cliff from short-run drift; they are not a claim of universal optimality.

## Work and memory

One representative mixed request exposes executor work, outside the latency acceptance loop:

| Strategy | Boxed keys | Semantic-key validations | Mask evaluations | Direct ID checks | Declared scratch B |
| --- | ---: | ---: | ---: | ---: | ---: |
| Common | 25 | 250 | 107 | 0 | 7688 |
| Full | 52 | 1957 | 52 | 0 | 37312 |
| Staged | 25 | 250 | 25 | 0 | 7688 |
| Lazy | 25 | 111 | 25 | 0 | 3712 |
| Subgroups | 25 | 250 | 25 | 0 | 7688 |
| Candidates | 25 | 250 | 0 | 25 | 4232 |
| Typed | 0 | 0 | 25 | 0 | 5504 |

Key counters count calls to generic semantic-key providers, not every machine comparison; Typed still compares posting pointers and integer boundary keys. Direct-check counters exclude Local fallback work and global cardinality proofs. Scratch totals describe fixed capacities, excluding the owned handle, slice headers, input/output storage and full Go stack frames. The one-operation work log is cold and is not an allocation/latency acceptance measurement.

The selected shared scratch is 3,456 bytes per borrowed object on this host,
plus bounded operand-local arrays. The work metric reports declared scratch
capacity, not a measured full Go stack or whole-process RSS. Scratch is borrowed
from a GC-discardable pool across index types and cleared before reuse. A cold
pool may allocate one object per simultaneously active chunk; the reported
zero steady allocations exclude neither cold acquisition nor initial output
growth, but amortize them over the fixed run length. They are not a guarantee
of zero allocation on a cold call. The fallback still uses
one Local for its entire call. Input slices and required output storage are
caller-owned and identical across the paired benchmark strategies.

On this workload, final warmed Auto runs report 0 B/op and 0 allocations/op in every 100-offer cohort, including unrelated requests. The same Local output-buffer baseline reports 807 B/11 allocations for mixed and 17,125 B/302 allocations for unrelated. Allocation profiles confirm that per-group escaping arrays and per-call Local handle allocation are gone; pool/bitmap state can still allocate on cold acquisition. Caller output growth is included and amortized, not forbidden.

## Verification and limits

Correctness compares ordered per-offer results against Local for every retained
strategy, append prefixes, length mismatches, empty batches, 129/300-offer calls,
0/1/64 and wide candidate sets, duplicate external IDs, wildcard and absent
values, non-comparable comparator values, Lossy, exclusions, nested schemas,
opaque/no-query-key test rules, many operands, observed contexts and concurrent
calls. The new batch oracles use Local.Search; existing project tests continue to
cover the legacy APIs independently.

Both release and candidate produce the same Local/Auto digest over 697 requests,
69,700 offers and 11,470 returned matches:
`d3b9ad65d45b8d15577575f445011a672c480c21cf7431a67fb884be3d17c7f6`.
The separate differential fixture also exercises all retained strategies.

`go test ./...` with the full dump and `go test -race ./...` passed. Final focused batch race tests, including concurrent calls, native wildcards and per-offer exclusions, passed after the additional fixtures and dispatch-floor change. golangci-lint v2.12.2 reports zero issues with Go 1.26.0 (its importer cannot decode Go 1.27 export data). New production coverage is 369/374 statements (98.7%); all production changes are new files, so this is diff coverage. The focused [coverage blocks](experiments/search-batch/coverage-new.txt) exclude unmodified source. Python runners pass Ruff and `git diff --check` passes. All changed files stay within 500 lines.

Results describe this dump-derived workload on a development laptop. Wide
anchors and schemas without a safe exact equality anchor deliberately retain
Local execution. The adaptive planner uses proven representation capabilities,
not a universally optimal cost model. The complete API migration remains a
separate decision after production-like validation of this experimental method.
