# v0.8.2 Local.Search versus batch-only Search

## Scope and revisions

This comparison measures complete 100-offer requests through the released
v0.8.2 Local API and the current batch-only API. It does not derive a speedup by
combining numbers from different studies. Production search code is unchanged.

- Baseline: v0.8.2, `7f32ddc6ccf6b12419a1efb5af1424ff64353cc0`.
  Each request acquires one Local, calls Local.Search for all 100 conditions,
  then closes it. Index.Search is never called in the measured baseline.
- Current: `8ec5e054014ed945e1871350b4d9c4e5c6f64f37`.
  Each request calls Index.Search(conditions, found) once. Its one private
  context is acquired and released inside the timed call.

Both builds use Go 1.27.3, Apple M1 Max, darwin/arm64, macOS 26.6.2,
GOMAXPROCS=1. Each release retains its own production dependencies, including
Roaring v2.4.4 versus v2.26.0. UUID v1.6.0 is added only to the old temporary
module for the common test harness. These are complete-version comparisons,
not attribution of the total improvement to batch analysis alone.

The supplied schema and complete 96,630-row dump are used on both sides. Dump
SHA256: `ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.
The parser rejects unsupported non-NULL indexed columns and ignores unindexed
columns. Neither version searches an empty or reduced index.

## Workloads and miss proofs

Every request has one customer, stable customer/activity parameters, 100 offers
and 1–3 changing store/slot fields between neighbors. Requests are deterministic
and byte-identical across versions. The same schema, generators, oracle and
measurement code are copied into both builds; only the small public-API adapter
changes. Fixture hashes and dependencies are recorded in
[metadata.json](experiments/v082-batch-search/metadata.json).

| Cohort | Requests | Definition |
| --- | ---: | --- |
| mixed | 64 | Original buyer/offer generator; already includes empty results. |
| tiny | 16 | Customers with 1–3 distinct modifier IDs before other predicates. |
| medium | 28 | Customers with 5–8 distinct modifier IDs before other predicates. |
| broad | 13 | Customers with 9–16 distinct modifier IDs before other predicates. |
| zero | 64 | Customer key absent from the complete dump. |
| storezero | 64 | Store key absent from the complete dump; buyer still present. |
| slotzero | 64 | Slot-type key absent from the complete dump; buyer still present. |
| intersectzero | 64 | Buyer/store/slot keys exist globally, but buyer/store combinations have no matches. |
| predicatezero | 64 | Equality and activity leave 1–4 matching rows; slot time rejects every one. |

The candidate-count labels refer to the customer posting's distinct modifier
IDs, not final result sizes or a promise that the two engines choose the same
internal anchor. Mixed has 5,874 empty offers out of 6,400 (91.8%), returning
624 IDs across the remaining offers. Dedicated miss cohorts contain only empty
outputs so their latency is visible separately.

Absent customer/store/slot values are explicitly proved absent by scanning the
dump. Existing-key intersections are verified empty against the scalar oracle.
For predicatezero, every query has a positive equality/activity candidate count
and a slot point covered by at least one real row elsewhere in the index. Thus
this is not an absent key or a globally empty interval posting. Each buyer's
request alternates two such slot points, changing one slot field at a time.
These are semantic stage proofs; the internal optimizer is free to reorder checks.

The dump has no populated exclusion columns. No result is attributed to actual
exclusion filtering, and the primary schema is not changed to invent that case.
All promised remaining-predicate rejections are exercised through slot time.
Forced-key miss generation can collapse originally different neighbors; the
fixture restores a one-second slot change in such pairs, preserving 1–3 field
changes. Both releases receive the resulting identical queries.

## Correctness

Both implementations are checked against an independent scalar evaluator over
original dump rows, grouped only by customer for efficiency. It checks equality,
activity containment, slot containment, duplicate-ID removal and first-insertion
ID order. It does not call another index method to compute expected matches.
Existing destination sentinel elements must survive each append.

Per-cohort workload and ordered-result SHA256 digests must match across both
binaries. The raw [baseline](experiments/v082-batch-search/v082-digest.json) and
[current](experiments/v082-batch-search/current-digest.json) reports also record
empty-offer counts and equality/activity candidate ranges. In total, the oracle
checks 441 requests, 44,100 offers and 1,204 returned IDs per version.

## Measurements

Three samples per version run in old/new/new/old/old/new order, with 80,000
complete requests per cohort per sample. Index construction, workload creation,
and two full cycles of output-buffer warming are outside timing. Each offer
has its own independent reused destination. Every timed operation creates and
closes its request context and performs analysis, matching and output appends.

Table values are medians of per-run metrics, not pooled quantiles. Mean is
Go ns/op for the complete operation, including common output resets and clock
instrumentation. p99.9 is the nearest-rank percentile of individual API calls.
The preallocated 100,000-element timing buffer does not grow at this run size.

| Cohort | Mean old → new, µs | Speedup | p99.9 old → new, µs | B old → new | Allocations old → new |
| --- | ---: | ---: | ---: | ---: | ---: |
| mixed | 74.605 → 12.350 | 6.04x | 170.542 → 30.041 | 3014 → 0 | 87 → 0 |
| tiny | 39.311 → 7.159 | 5.49x | 74.791 → 19.875 | 936 → 0 | 10 → 0 |
| medium | 89.181 → 13.252 | 6.73x | 142.834 → 30.750 | 968 → 0 | 12 → 0 |
| broad | 92.059 → 15.710 | 5.86x | 206.375 → 34.166 | 11101 → 0 | 387 → 0 |
| zero | 10.345 → 1.494 | 6.92x | 25.333 → 1.750 | 24 → 0 | 1 → 0 |
| storezero | 14.271 → 1.551 | 9.20x | 30.792 → 1.792 | 24 → 0 | 1 → 0 |
| slotzero | 17.556 → 1.427 | 12.30x | 36.667 → 1.625 | 24 → 0 | 1 → 0 |
| intersectzero | 47.537 → 4.243 | 11.20x | 97.708 → 10.625 | 1979 → 0 | 52 → 0 |
| predicatezero | 110.961 → 19.923 | 5.57x | 223.542 → 40.041 | 1970 → 0 | 52 → 0 |

These are warmed complete-request measurements on a developer machine. They
include context lifetime costs but do not measure cold process startup, service
network overhead or a production SLA. Wide retained caches across multiple
separate requests are not modeled: v0.8.2 uses a fresh Local handle per request,
and current Search owns one context per request. No Index.Search single-query
or permanently warm shared-Local baseline is substituted.

## Reproduction and checks

```sh
RULEIX_PRODUCTION_DUMP=/absolute/path/to/full-dump.json \
  python3 docs/experiments/v082-batch-search/run.py
python3 docs/experiments/v082-batch-search/summarize.py \
  "$(cat /tmp/ruleix-v082-batch-last)"
```

The runner archives both exact commits and builds outside the working tree.
All shared fixture hashes agree after gofmt, with the API adapter recorded
separately. Result and query digests must agree before benchmarks start. Run the
comparison alone, without concurrent benchmarks, tests or lint. Raw samples,
correctness logs and metadata are retained beside the runner.

Both dedicated correctness suites and temporary benchmark builds pass. Python
sources parse, shared Go fixtures are gofmt-normalized before both builds, and
the repository test suite and whitespace/file-size checks pass. This task
changes only comparison artifacts and documentation; production diff coverage
is not applicable. No release or push is part of the comparison.
