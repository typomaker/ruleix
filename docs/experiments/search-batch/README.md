# SearchBatch experiment bundle

The maintained contract and conclusions are in [the batch study](../../search-batch.md).
The baseline is v0.9.0 / d47c5a0. Candidate source hashes and shared fixture
hashes are in `metadata.json`; `snapshot/` pins the exact four added production
files and minimal test harness used for the final measurements. The original
production files and dependencies come from the baseline commit. This bundle
contains no database dump.

## Reproduction

Use Go 1.27.3 and Python 3.12 or newer; the measured host was M1 Max,
macOS 26.6.2, darwin/arm64. Supply the original 96,630-row dump, hash recorded
in metadata. Timed processes use GOMAXPROCS=1.

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
python3 docs/experiments/search-batch/run.py --verify-only
python3 docs/experiments/search-batch/run.py --iterations 40000 --screen 3000
python3 docs/experiments/search-batch/summarize.py /printed/scratch/path
python3 docs/experiments/search-batch/profile.py /printed/scratch/path
```

The default runner uses pinned snapshots when present. `--snapshot PATH` can
select a historical snapshot; a nonexistent path selects working-tree source.
Verification-only output has its own `/tmp/ruleix-search-batch-verify-last`
pointer and does not replace the last timing-run pointer.

Both binaries use byte-identical workload/schema/digest fixtures. A test-only
baseline bridge supplies unused batch entry points by delegating to Local;
the compatibility timing baseline invokes the actual public Local.Search.
The candidate bridge exposes internal variants only in tests. All primary
Local/Auto comparisons run in the same candidate binary. Normal builds have
only SearchBatch as the additional public method.

## Evidence

- `primary-*`: Local/Auto/Auto/Local/Local/Auto, 3 x 40,000 operations per
  strategy and cohort; each operation has 100 offers.
- `short-*`: the same interleaving, 20,000 operations, including 0/1/2/3/4/16
  offers. Calls below 16 use the Local core and owned stack handle.
- `strategies-*`: forward/reverse strategy screens at 3,000 operations.
- `compatibility-*`: release/current/current/release public Local controls,
  20,000 operations. Existing production search source is unchanged.
- `work.txt`: one cold diagnostic operation with executor counters; not a
  steady-state allocation or tail measurement.
- `summaries/`: per-run metric medians, never pooled percentiles.
- `*-digest.json`: exact Local/Auto result digest, equal across both binaries.
- `coverage-new.txt`: coverage blocks only for the four new production files.
- `profiles/final/`: focused final CPU/allocation summaries. Filtering on
  searchProductionBatch removes index-loading/setup stacks from the displayed
  nodes; percentages still use total profile samples/bytes as denominator.
- `shapes-*`: final interleaved synthetic controls over candidate counts
  1/8/16/32/64/65/256/4096 and 1/2/4/16/100 offers. Source is batch_shapes_test.go;
  both strategies verify ordered results and empty outputs before timing.

Profile benchmarks use 1s runs and allocation sampling at 4096 bytes. The raw
profiles and binaries stay in the printed scratch directory; text summaries
and binary hashes are archived here. Profiled timings are diagnostic rather
than the latency acceptance data. Very long profile runs can grow the latency
sample buffer; focused allocation summaries exclude that benchmark bookkeeping.

## Historical investigations

Historical folders are diagnostic snapshots, not the final API performance
claim. They establish causes and preserve intermediate controls:

- `before-short-guard/`: source/fixture snapshot and short controls before
  avoiding analysis on very small calls.
- `before-capability-guard/`: ordinary Between repeatedly walked ordered
  indexes for 32 candidates, despite Local's range caching. Focused profiles
  identified the additional ID checks. The final dispatcher uses Build's native
  mask capability and preserves Local execution for this representation.
- `before-context/`: per-call Local handle allocation before stack ownership.
- `before-short-floor/`: short/fallback controls before the conservative
  16-offer floor.
- Top-level initial profile summaries show full/lazy plan slabs and boxed
  keys, then escaping per-subgroup arrays before scratch reuse. The final
  pooled Full plans, bounded Lazy entries and typed keys are separately profiled.

An initial shape screen's `empty` label used reversed query bounds, so it did
not guarantee empty output and is not empty-path acceptance evidence. Final
shape controls use the point 200/200 beyond every stored upper bound and assert
that Local returns no IDs. Earlier strategy-map construction was also removed
from the test bridge; final variant screens use an allocation-free switch.
