# Final v0.9.0 release checks

The baseline is 453f87d (production code a07b9dd); the candidate is the final
v0.9.0 tag. Both use identical eight shared fixtures and the supplied 96,630-row
dump, Go 1.27.3, Apple M1 Max, darwin/arm64 and GOMAXPROCS=1. Temporary minimal
source trees use the harness prepared by `../v082-early-empty/run.py`; copy the
release's changed production Go files into a copy of that runner's `current`
source tree and compile with `go test -c`. No benchmark fixture differs.

After race and compilation finished, samples alternate baseline/release/release/
baseline, 20,000 operations per sub-benchmark, with `-test.run '^$'
-test.bench '^BenchmarkEmpty(Matrix|Short|PublicControls)$'
-test.benchtime=20000x -test.benchmem`. The environment supplies
`RULEIX_PRODUCTION_DUMP`. Matrix and public controls use 100 offers; short
controls use the indicated prefix. Matrix includes acquisition/Close; the
WarmLocal control reuses a populated Local. Values below average two samples
and use microseconds per operation; this short check is not a new tail study.

| Control | Before release gate | Final release |
| --- | ---: | ---: |
| Matrix/zero | 6.194 | 6.176 |
| Matrix/storezero | 6.053 | 6.000 |
| Matrix/slotzero | 4.718 | 4.702 |
| Matrix/intersectzero | 19.855 | 19.364 |
| Matrix/alternating | 23.648 | 23.561 |
| Matrix/mixed | 32.044 | 31.959 |
| Matrix/tiny | 19.459 | 19.252 |
| Matrix/medium | 32.453 | 32.185 |
| Matrix/broad | 46.124 | 46.158 |
| Matrix/rotate | 36.242 | 36.186 |
| PublicControls/Index | 106.225 | 105.823 |
| PublicControls/WarmLocal | 30.479 | 30.509 |
| Short/zero/1 | 0.260 | 0.260 |
| Short/zero/4 | 0.443 | 0.476 |
| Short/zero/16 | 1.183 | 1.250 |
| Short/mixed/1 | 2.124 | 2.097 |
| Short/mixed/4 | 3.868 | 3.714 |
| Short/mixed/16 | 7.535 | 7.461 |

All byte/allocation metrics agree exactly across the two versions in these
controls. Raw percentile measurements remain in `measurements.txt`; the earlier
three-sample release comparison is the primary tail-latency study. Ordinary,
race and dump differential tests pass; golangci-lint v2.12.2 with Go 1.26.0
reports zero issues. The benchmark helpers also pass a one-iteration smoke run
for CompactBetween/One/Index/Compact and BetweenIDExperiment/One/Index/Table.
Diff coverage is recorded in `diff-coverage.json`; reproduce it with
`python3 docs/experiments/release-0.9.0/diff-coverage.py COVERPROFILE`.

The initial short-prefix averages include a noisy candidate sample. A focused
follow-up interleaves baseline/release/release/baseline/baseline/release, with
1,000,000 operations per sub-benchmark and the same environment, using
`-test.bench '^BenchmarkEmptyShort$/^zero$/^(4|16)$'`. Median means are
456.0 → 451.8 ns for four empty offers and 1162 → 1163 ns for sixteen;
both allocate 24 bytes and one object. This does not reproduce the apparent
short-prefix slowdown in the initial two-sample averages. Raw follow-up data
is in `short-controls.txt`.
