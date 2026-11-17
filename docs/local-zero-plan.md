# Empty Local plan lookup investigation

## Scope and reproduced cause

The baseline is bf89413, whose production code is the portable Go
revision 086a91b. The production comparison uses v0.8.2 at
7f32ddc6ccf6b12419a1efb5af1424ff64353cc0. The candidate adds two
empty-plan guards to `all_planning.go`. Public APIs, Build, rule
representations and Local storage layouts are unchanged.

Environment: Apple M1 Max, macOS 26.6.2, Go 1.27.3,
darwin/arm64, GOMAXPROCS=1. Workload: the same 96,630-row dump,
production schema, 100 related offers through one Local per request.
The missing-customer fixture and dump hash are documented in the
[production comparison](production-v082-portable-comparison.md).

A customer absent from the dump makes the equality estimate zero.
Ranking returns before `rememberLocalPlan`, so the Local's `allPlans`
map remains nil for its entire lifetime. Nevertheless, each offer
previously attempted these three lookups:

1. `loadLocalQueryResult` reads `pool.allPlans[r]`.
2. `reuseLocalPlan` reads the same entry through `localPlan`.
3. `rankChildren` reads it again to decide whether to refresh a plan.

The third lookup was added in 2eb4fec. v0.8.2 has the first two.
Repeated empty offers never advance beyond this cold path. These
lookups cannot return a plan, yet they invoke Go's generic map
access. In this toolchain an empty `map[any]` access checks the
dynamic key's comparability through `mapKeyError2` even with a nil
map. The key is a rule pointer and is valid; no error occurs. This
is runtime validation overhead, not an error-handling failure or
bitmap work. Nil map accesses do not hash the key.

The runtime implementation is available locally in
`src/internal/runtime/maps/runtime.go` and `map.go` in the Go SDK.
The original 3,000,000-request CPU profile attributes 4.49 s
cumulatively to `mapKeyError2`, including 4.26 s self time. The
source profile attributes 0.91 s to the additional refresh lookup.
The profile also contains startup, sample sorting and GC outside
the measured benchmark loop, so these percentages are not a
latency breakdown of a request.

## Causality controls and correction

Interleaved 120,000-request runs of 2eb4fec and its immediate parent
(d0e6daa) reproduced part of the added cold-path cost: median mean
11.302 → 11.783 µs. This does not attribute every intervening
release change to that one commit.

An isolated guard on only the additional refresh lookup moved the
current mean 11.764 → 11.416 µs in three interleaved runs. Guarding
nil lookups inside `localPlan` and routing the query-result lookup
through it moved the mean to 10.956 µs, while still calling the
reuse function. The selected correction skips the reuse block
before a map exists, and returns immediately from the query-result
lookup when that map is nil. Thus it also avoids entering the reuse
helper on an impossible cache hit. Successful ranking still creates
a plan normally; a non-nil map follows the existing lookup paths.

The selected 120,000-request controls alternate production,
baseline, candidate, candidate, baseline, production, production,
baseline, candidate. Values are medians of three per-run metrics.
Each request still contains 100 Local searches.

| Metric | v0.8.2 | Baseline | Guarded |
| --- | ---: | ---: | ---: |
| Mean | 10.552 µs | 11.612 µs | 10.514 µs |
| p99 | 11.542 µs | 12.875 µs | 11.500 µs |
| p99.9 | 25.958 µs | 27.542 µs | 25.458 µs |
| Bytes / allocations | 32 / 1 | 32 / 1 | 32 / 1 |

The correction lowers mean about 9.5% and restores parity with the
production baseline in these controls. It removes inherited cold
lookup work as well as the additional lookup. It is not proof that
the third lookup alone explains the entire release-to-release gap.
The 32 B/op includes the benchmark's duration-sample storage growth
at 120,000 iterations; the earlier 80,000-request controls report
24 B/op. No search allocation was added or removed.

A 1,200,000-request candidate CPU profile no longer has samples in
`mapKeyError2`, `reuseLocalPlan` or `localPlan` on this workload.
The two profiles have different iteration counts and should not be
compared by raw seconds. The disappeared calls follow directly from
the guarded control flow; source cardinality checks still run for
every offer and handle changing query values.

## Nonempty controls and verification

The initial nonempty controls overlapped tool compilation and showed
small shifts in several means. They are archived but are not used to
attribute a regression. A quiet follow-up repeats baseline, guarded,
guarded, baseline, with 80,000 requests per cohort and run. Values
are medians of two per-run measurements, in µs.

| Cohort | Mean baseline → guarded | p99.9 baseline → guarded | B / allocations |
| --- | ---: | ---: | ---: |
| Mixed | 31.456 → 31.167 | 86.875 → 79.375 | 845 / 12 |
| 1–3 | 18.919 → 18.926 | 57.042 → 55.021 | 776 / 10 |
| 5–8 | 31.397 → 31.304 | 76.708 → 72.146 | 819 / 11 |
| 9–16 | 44.314 → 44.302 | 94.438 → 91.688 | 1,015 / 19 |
| Rotating | 35.366 → 35.358 | 83.292 → 79.750 | 845 / 12 |

Nonempty means remain within about 1%, with unchanged allocation
counts and bytes in each cohort. No layout, allocation or result
ownership changes are introduced by the guards.

`TestLocalEmptyPlanTransitions` verifies repeated misses without
creating a plan, then hits and misses in the same Local, preservation
of an existing destination prefix and reuse after Close. The full
suite passed with 91.9% statement coverage; both modified executable
lines are covered (100% changed-line coverage). The guarded dump
binary passed the 12,800-offer differential test, the unchanged
cross-version digest, and 6,400 missing-customer checks against both
Local and Index. The fix retains portable Go and does not identify
customer properties or add schema-specific logic.

Go formatting, shell syntax and Git whitespace checks passed.
The installed golangci-lint v1 cannot read this repository's v2
configuration. The pinned v2.12.2 tool cannot read Go 1.27 export
data, so lint was run with the installed Go 1.26.9 toolchain:

```sh
GOTOOLCHAIN=go1.26.9 GOMAXPROCS=2 \
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 \
  run --new-from-rev=bf89413
```

Changed-code lint passed with zero issues. Full lint reported 40
findings, all in files byte-for-byte unchanged from bf89413; these
pre-existing findings were verified against Git and left outside
this correction. Tests and performance measurements use Go 1.27.3.

A subsequent [early-empty study](local-early-empty.md) evaluates all three
follow-up ideas and accepts prepared checks with a per-Local rejection hint.

## Reproduction

The shared runner now accepts baseline and current revisions. After
checking out the fix, run:

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
RULEIX_BASELINE_REVISION=bf89413 RULEIX_CURRENT_REVISION=HEAD \
  RULEIX_COMPARISON_SMALL=1 \
  sh docs/experiments/production-comparison/run.sh > /tmp/zero-plan.log 2>&1
python3 docs/experiments/production-comparison/summarize.py /tmp/zero-plan.log
```

The runner archives both revisions, copies identical dump fixtures,
checks result digests, and alternates timing runs. Omit
`RULEIX_COMPARISON_SMALL=1` for the original complete workload and
1–4, 5–8, 9–16 and rotating controls. The selected correction's
1–3 controls use the small-cohort fixture.

For CPU profiles, run a compiled comparison binary with
`RULEIX_PLANNER_BUCKET=zero GOMAXPROCS=1` and:

```sh
./version.test -test.run '^$' \
  -test.bench '^BenchmarkProductionDumpLocalRequest100$' \
  -test.benchtime=1200000x -test.cpuprofile=/tmp/zero.cpu
go tool pprof -top -nodecount=0 ./version.test /tmp/zero.cpu
```

[Archived controls and CPU summaries](experiments/local-zero-plan/)
retain the individual measurements, including the initial nonempty
runs collected while checks were active. The report distinguishes
those from the quiet follow-up controls.
