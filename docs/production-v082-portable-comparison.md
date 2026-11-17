# Production v0.8.2 versus portable Go

## Revisions and environment

This comparison treats v0.8.2 as the production version designated
in the prior discussion. It does not inspect a running deployment.
The tag resolves to commit 7f32ddc6ccf6b12419a1efb5af1424ff64353cc0.
The current portable Go implementation is 086a91b.

Both binaries use Go 1.27.3 on Apple M1 Max, macOS 26.6.2,
darwin/arm64. Timed processes use GOMAXPROCS=1.
Each release retains its own production dependencies, including
Roaring v2.4.4 in v0.8.2 and v2.26.0 in 086a91b.
The temporary tag checkout adds github.com/google/uuid v1.6.0 solely
for the shared test harness. Its other dependencies remain as released.

Both checkouts use byte-identical copies of the current three dump
test files, digest test and short/reopened probes. Shared test files
have a go1.24 build constraint so the modern benchmark loop is valid
in the tag's go1.23 module. Production search code is untouched.

## Workload and correctness

The dump has 96,630 rows and file SHA-256
`ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.
Both indexes use the supplied production
schema, including the parser's checks for unsupported populated
indexed columns. Each request selects one customer and executes
100 related offers through one Local, then closes that Local.
Adjacent grouped offers change 1–3 store/slot fields while customer
parameters remain stable. The generator selects 64 customers.
The 1–4, 5–8 and 9–16 cohorts have 23, 28 and 13 customers.
The rotated control uses the same offers in stride-37 order.

Each release passed the existing Local-versus-Index differential
test for all 12,800 grouped and rotated offers. An additional
cross-version digest sorts the UUID results within each offer,
includes their count, then hashes every result. It preserves
multiplicity and the offer sequence. Both releases produced
1,248 matches across 128 requests and 12,800 offers with SHA-256

```text
0ef4f277cac13b64449e69d6880f0b5a8a7d421670438ec141a20667d9784376
```

## Complete requests

Three runs per release alternate old, current, current, old, old,
current. Mixed samples use 80,000 requests. Other cohorts use
50,000. Each table value is the median of the three per-run
measurements. Latencies are µs per complete 100-offer request.

| Cohort | Metric | v0.8.2 | 086a91b |
| --- | --- | ---: | ---: |
| Mixed | Mean | 74.915 | 30.960 |
| Mixed | p99 | 131.333 | 54.125 |
| Mixed | p99.9 | 200.792 | 79.167 |
| 1–4 | Mean | 44.814 | 20.256 |
| 1–4 | p99 | 71.167 | 29.375 |
| 1–4 | p99.9 | 95.666 | 54.875 |
| 5–8 | Mean | 88.492 | 31.468 |
| 5–8 | p99 | 116.458 | 45.167 |
| 5–8 | p99.9 | 150.458 | 73.208 |
| 9–16 | Mean | 93.312 | 43.975 |
| 9–16 | p99 | 155.166 | 59.333 |
| 9–16 | p99.9 | 499.041 | 99.750 |
| Rotated | Mean | 76.920 | 35.289 |
| Rotated | p99 | 135.125 | 55.709 |
| Rotated | p99.9 | 209.583 | 80.666 |

Mixed mean is 2.42x faster, a 58.7% latency reduction. Mixed
p99.9 is 2.54x faster, a 60.6% reduction. Broad p99.9 is 5.00x
faster. Mixed p99.9 samples span 200.791–253.791 µs on v0.8.2
and 76.750–80.667 µs on current. Broad samples span
471.000–506.333 µs and 93.333–101.500 µs, respectively.

| Cohort | Bytes/request old → current | Allocations/request old → current |
| --- | ---: | ---: |
| Mixed | 3,053 → 845 | 89 → 12 |
| 1–4 | 956 → 780 | 11 → 10 |
| 5–8 | 1,011 → 819 | 13 → 11 |
| 9–16 | 11,159 → 1,015 | 389 → 19 |
| Rotated | 2,866 → 845 | 88 → 12 |

## Build and storage

Two samples per release alternate old, current, current, old.
Build and retained-index probes use 10x. Fresh and reopened Local
probes use 1000x. Values below average the two samples.

| Metric | v0.8.2 | 086a91b |
| --- | ---: | ---: |
| Build mean | 160.08 ms | 150.50 ms |
| Transient Build bytes | 4,734,811 | 4,784,976 |
| Build allocations | 45,672 | 46,369.5 |
| Retained index bytes | 2,585,668 | 2,583,619.5 |
| Fresh Local bytes allocated | 7,183 | 3,829 |
| Fresh Local allocations | 106 | 26 |
| Fresh Local bytes retained | 4,711 | 3,427 |
| Reopened Local bytes retained | 4,733 | 3,431 |
| Reopened Local allocations | 92 | 15 |

Build time improves about 6.0%. Transient Build bytes and allocation
count increase about 1.1% and 1.5%. Retained index measurements are
close. Fresh retained Local memory falls about 27.3% and reopened
retained memory about 27.5%.

Short-request samples use 50,000 requests and prefixes of the same
mixed workload. Mean values average two paired runs.

| Offers | Mean old → current | Bytes old → current | Allocations old → current |
| --- | ---: | ---: | ---: |
| 1 | 2.929 → 2.391 µs | 984 → 832 | 11 → 12 |
| 4 | 7.705 → 4.019 µs | 1,095 → 835 | 17 → 12 |
| 16 | 17.786 → 7.808 µs | 1,353 → 836 | 26 → 12 |

The single-offer prefix has one extra reported allocation in current,
despite lower mean time and fewer allocated bytes. This comparison
records that difference and does not modify either executor.

## Follow-up: zero and 1–3 candidates

The same revisions, environment and dump were measured in three
interleaved runs per release, 80,000 complete 100-offer requests per
run. Medians of per-run metrics follow. Candidate count here means
the number of distinct constraint UUIDs associated with the customer,
as in the earlier cohort selector, before other offer predicates.
The 1–3 cohort contains 16 of the original 64 customer requests.

The original generator seeds customers from existing dump rows, so
it cannot produce zero candidates. The zero control copies all 64
request shapes and substitutes one customer UUID verified absent
from the dump. It measures a missing-customer lookup, rather than
an offer whose other filters eliminate an existing customer's rows.
There is no combined 0–3 figure because the production frequency
of missing-customer requests is unknown.

| Candidates | Metric | v0.8.2 | 086a91b |
| --- | --- | ---: | ---: |
| 0 | Mean | 10.279 µs | 11.627 µs |
| 0 | p99 | 11.250 µs | 12.750 µs |
| 0 | p99.9 | 25.500 µs | 28.250 µs |
| 0 | Bytes / allocations | 24 / 1 | 24 / 1 |
| 1–3 | Mean | 38.948 µs | 18.887 µs |
| 1–3 | p99 | 53.709 µs | 25.209 µs |
| 1–3 | p99.9 | 85.125 µs | 55.792 µs |
| 1–3 | Bytes / allocations | 952 / 11 | 776 / 10 |

For 1–3 candidates, mean improves 2.06x and p99.9 improves 1.53x.
The zero-candidate mean regresses 13.1% and p99.9 regresses 10.8%.
Zero mean samples span 10.271–10.422 µs on v0.8.2 and
11.564–11.703 µs on current. This is a reproduced release-to-release
regression in the measured 086a91b revision. A subsequent
[empty-plan investigation and correction](local-zero-plan.md) restored
parity; the original comparison values above remain unchanged.
Both versions additionally verified empty results for 6,400
missing-customer offers through Local and Index. The existing
12,800-offer result digest remains identical across versions.

To reproduce the follow-up, use the runner with
`RULEIX_COMPARISON_SMALL=1`; the archived patch changes only the
shared cohort selector. It selects 1–3 candidates and adds the
missing-customer control, retaining the same request timer and
Local lifecycle. The result fixture also verifies zero-candidate
correctness in this mode.

## Reproduction and interpretation

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
sh docs/experiments/production-comparison/run.sh > /tmp/ruleix-comparison.log 2>&1
python3 docs/experiments/production-comparison/summarize.py /tmp/ruleix-comparison.log
```

The runner creates temporary checkouts, freezes current sources at
086a91b, retains release dependencies and copies the same harness
into both. It verifies result digests before timing. The archived
summarizer reports medians and individual samples.

The measurements compare complete releases, including dependency
changes. They do not isolate any individual optimization. They use
the dump-derived customer/offer generator on a development M1 Max,
so the percentages describe this workload and environment.
The baseline production-version assumption should be checked against
the deployment manifest when applying these numbers operationally.

The digest fixture and benchmark sources compiled and ran on both
versions. Shell syntax, Python lint and Git whitespace checks passed.
This comparison changes documentation and the reproducible test
harness.
