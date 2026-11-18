# v0.8.2 Local versus current batch Search

Maintained findings and limitations: [comparison report](../../production-v082-batch-comparison.md).

`run.py` archives v0.8.2 and 8ec5e05, builds minimal test binaries, proves
identical common fixture hashes, verifies scalar-oracle result/query digests,
and interleaves three 80,000-request samples per version. Each request has one
buyer, 100 related offers and one context. Run alone with the full JSON path in
`RULEIX_PRODUCTION_DUMP`. It prints its temporary source/binary directory.

`generator.go.txt` preserves the 8ec5e05 buyer/slot generator; schema and basic
cohorts are fetched directly from that exact commit. `comparison.go.txt` adds
common timing, append/order checks, the scalar oracle and miss invariants.
`predicate-miss.go.txt` builds interval rejections from real dump rows. Every
compiled fixture has the go1.24 build constraint for the modern benchmark loop;
its body is identical on both releases, except for the separate public adapter.
Go formatting is normalized before source hashing and compilation.

`v082-digest.json` and `current-digest.json` are identical, with per-cohort
workload/result hashes and stage counts. The correctness logs prove both
binaries passed the oracle. Both use all 96,630 input rows. The nine cohorts
cover 441 distinct requests, 44,100 offers and 1,204 matching IDs per version.
The metadata records exact revisions, fixture SHA256 hashes, environment,
request count and each build's complete module list.

`measure-0-v082`, `measure-1-current`, `measure-2-current`, `measure-3-v082`,
`measure-4-v082`, `measure-5-current` are the complete ordered measurement logs.
Each contains all nine cohorts; no slow cohort or unfavorable sample is omitted.
`summarize.py` produces `summary.json` and the maintained table using medians
of three per-run means/tails. It rejects incomplete sample sets. Current warmed
means improve in every tested cohort; these artifacts claim no service SLA.

Latest M1 Max / Go 1.27.3 / GOMAXPROCS=1 run: mixed old/new mean
74.605/12.350 µs, p99.9 170.542/30.041 µs, 3,014/0 B and 87/0 allocations.
See the report for the full miss table and the absence of real exclusion data.
The root test log and verification record cover this documentation/harness-only
task; no production code or public API changes are included.
