# v0.8.2 Local.Search versus prepared short-batch Search

## Revisions and method

Baseline: v0.8.2, `7f32ddc6ccf6b12419a1efb5af1424ff64353cc0`.
Current: `9c95f7d653f645bb955abc49bc64bd0e44511520`.
This is a fresh direct comparison; no speedup is calculated by combining
measurements from earlier studies. Production search code is unchanged here.

The baseline acquires one Local per request, loops through its conditions with
Local.Search, and closes it. Its Index.Search is never called. Current performs
one Index.Search(conditions, found), including its private context lifetime.
Every offer has an independent destination with identical warm-up/reuse rules.

Both binaries use Go 1.27.3, Apple M1 Max, darwin/arm64, macOS 26.6.2,
GOMAXPROCS=1. They retain their released production dependencies, including
Roaring v2.4.4 versus v2.26.0. UUID v1.6.0 is added only to the old temporary
module for the shared fixture. Results compare complete released versions,
not the isolated contribution of the latest short-batch change.

Both load the same complete 96,630-row supplied index, schema and JSON dump:
SHA256 `ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.
Unindexed columns are ignored. Unsupported populated indexed columns would be
rejected by the parser; the provided dump passes that validation.

## Workloads and correctness

Lengths are 5, 8, 15, 16 and 100. Each shorter request is the corresponding
prefix of the same 100-offer request on both versions. One buyer and its
activity parameters stay fixed; 1–3 store/slot properties change between
neighbors. Forced misses restore a one-second slot difference where replacing
a key would otherwise collapse two neighbors into an identical condition.

Nine modes are tested at every length:

- Mixed: 64 buyer requests.
- Tiny/medium/broad: 16/28/13 buyers with respectively 1–3/5–8/9–16 distinct
  customer-posting modifier IDs before other predicates.
- Missing customer, store or slot type: each key is proved absent from the dump.
- Existing-key empty intersection: individual keys exist, their combination does not.
- Remaining-predicate rejection: equality/activity have candidates, all fail
  slot time; those slot points still match real rows elsewhere in the index.

These definitions and the independent scalar evaluator come from the pinned
[earlier v0.8.2 comparison](production-v082-batch-comparison.md). The evaluator
uses original rows, deduplicates IDs and sorts by first-insertion rank. Existing
destination sentinels must survive appending. Ordered result and workload hashes
match across both binaries for all 45 cases: 2,205 requests and 63,504 offers per
version. Correctness reports include each mode/length's result and miss counts.
There is no empty-index substitution. Real exclusion filtering is not claimed:
exclusion columns in this supplied dump are NULL.

## Timed requests

Three samples per revision run old/new/new/old/old/new. Every case uses 40,000
complete requests per sample. Setup, Build and two full output-warming cycles
are outside timing; acquisition, search and release remain timed. The fixed
100,000-element timing buffer does not grow. Means include common buffer-reset
and clock instrumentation; p99.9 uses nearest-rank individual API samples.
Tables show medians of per-run metrics, not pooled quantiles.

| Cohort | Mean old → new, µs | Speedup | p99.9 old → new, µs | B old → new | Allocations old → new |
| --- | ---: | ---: | ---: | ---: | ---: |
| mixed/5 | 8.658 → 2.607 | 3.32x | 40.500 → 6.750 | 1087 → 0 | 16 → 0 |
| mixed/8 | 11.583 → 2.996 | 3.87x | 45.667 → 7.958 | 1137 → 0 | 18 → 0 |
| mixed/15 | 16.773 → 3.891 | 4.31x | 54.666 → 11.625 | 1309 → 0 | 24 → 0 |
| mixed/16 | 17.267 → 3.997 | 4.32x | 52.584 → 10.042 | 1324 → 0 | 25 → 0 |
| mixed/100 | 74.365 → 12.401 | 6.00x | 170.625 → 31.375 | 3014 → 0 | 87 → 0 |

The 5–15-condition current path uses a Build-proved exact bounded source and
early complete-batch key-miss proofs. Other unsupported short shapes fall back
to the established private kernel. The 16/100-condition rows use the normal
batch dispatcher. No later experimental value-reuse candidate is included.

Misses at the shortest and longest tested sizes:

| Cohort | Mean old → new, µs | Speedup | p99.9 old → new, µs | B old → new | Allocations old → new |
| --- | ---: | ---: | ---: | ---: | ---: |
| zero/5 | 0.742 → 0.329 | 2.25x | 1.250 → 0.417 | 24 → 0 | 1 → 0 |
| zero/100 | 10.316 → 1.489 | 6.93x | 22.000 → 1.667 | 24 → 0 | 1 → 0 |
| storezero/5 | 0.965 → 0.479 | 2.01x | 1.667 → 0.584 | 24 → 0 | 1 → 0 |
| storezero/100 | 14.321 → 1.557 | 9.20x | 32.458 → 2.167 | 24 → 0 | 1 → 0 |
| slotzero/5 | 1.108 → 0.477 | 2.32x | 1.875 → 0.584 | 24 → 0 | 1 → 0 |
| slotzero/100 | 17.454 → 1.421 | 12.28x | 36.250 → 1.625 | 24 → 0 | 1 → 0 |
| intersectzero/5 | 4.274 → 0.786 | 5.44x | 31.458 → 1.083 | 1040 → 0 | 14 → 0 |
| intersectzero/100 | 47.508 → 4.237 | 11.21x | 96.125 → 10.917 | 1979 → 0 | 52 → 0 |
| predicatezero/5 | 8.005 → 2.488 | 3.22x | 37.334 → 5.417 | 1031 → 0 | 13 → 0 |
| predicatezero/100 | 110.406 → 19.800 | 5.58x | 219.417 → 39.000 | 1970 → 0 | 52 → 0 |

Candidate-count controls at 15 and 100 conditions:

| Cohort | Mean old → new, µs | Speedup | p99.9 old → new, µs | B old → new | Allocations old → new |
| --- | ---: | ---: | ---: | ---: | ---: |
| tiny/15 | 8.514 → 2.256 | 3.77x | 35.084 → 3.875 | 936 → 0 | 10 → 0 |
| tiny/100 | 38.791 → 7.094 | 5.47x | 77.917 → 18.083 | 936 → 0 | 10 → 0 |
| medium/15 | 18.644 → 3.709 | 5.03x | 50.959 → 9.708 | 968 → 0 | 12 → 0 |
| medium/100 | 88.094 → 13.263 | 6.64x | 141.333 → 31.083 | 968 → 0 | 12 → 0 |
| broad/15 | 19.614 → 3.915 | 5.01x | 63.125 → 10.458 | 2706 → 0 | 77 → 0 |
| broad/100 | 91.203 → 15.403 | 5.92x | 193.958 → 35.041 | 11101 → 0 | 387 → 0 |

The complete 45-case mean/p99/p99.9/byte/allocation table and per-run logs are
in the [evidence directory](experiments/v082-short-batch/evidence/comparison.md).
All 45 current cases report 0 B/op and 0 allocations/op after warm-up. Old mixed
requests allocate 1,087–3,014 B and 16–87 objects across the tested sizes.
All measured means and median p99.9 estimates improve against v0.8.2; no
ambiguous losing control required a focused repeat. Retained memory and Build
cost are outside this request-latency comparison.

## Reproduction and limits

```sh
RULEIX_PRODUCTION_DUMP=/absolute/path/to/full-dump.json \
  python3 docs/experiments/v082-short-batch/run.py
python3 docs/experiments/v082-short-batch/summarize.py \
  "$(cat /tmp/ruleix-v082-short-last)"
```

The runner archives the exact revisions and shares all test fixtures byte for
byte after gofmt. Only the public API adapter differs. SHA256 manifests,
module lists, toolchain/platform, dump hash, ordered query/result digests and
raw interleaved logs are retained. The full JSON is not committed.

This is a developer-machine comparison with warmed resources. It does not
measure cold process startup, network latency or a service SLA. No test/lint
jobs from this task run concurrently with timing. Unrelated desktop/container
load is not under this runner's control; tails are estimates from the recorded
sessions, and noisy controls must be interpreted using focused repeats.

Both temporary test builds and independent correctness suites pass. The
repository test suite, Python syntax, generated fixture formatting, whitespace
and changed-file 500-line checks pass. This task changes no production code;
new production diff coverage is not applicable. No push or release is included.
