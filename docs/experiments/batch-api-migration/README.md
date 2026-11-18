# Batch API migration evidence

See [the maintained report](../../batch-api-migration.md) for contracts, results,
interpretation and limitations. This directory compares acaa3db SearchBatch with
the batch-only Index.Search API, on the complete 96,630-row dump.

- `run.py`: minimal paired binaries, identical acaa3db workload fixtures adapted
  only at API selectors, ordered digest verification and interleaved timing.
  Candidate production files come from the working tree; hashes pin the measured
  revision in `metadata.json`. It also collects CPU and allocation profiles for
  the investigated empty-intersection tail. Raw `.cpu`/`.mem` files remain in the
  printed scratch directory; text profile summaries are retained here.
- `assembly.py` and `assembly-summary.json`: count/compare operation sequences
  in the compiled core. Identical opcodes are supporting evidence only; generic
  dictionary offsets and relocation operands change after method removal.
- `primary-*`: three 40,000-request samples per revision, eight 100-offer cohorts.
- `short-*`: two 20,000-request samples per revision, lengths 0/1/4/16/100,
  mixed/zero/storezero/slotzero/intersectzero/unrelated cohorts.
- `empty-confirm-*`: three 400,000-request samples per revision for missing
  stores and existing-key empty intersections. Benchmark sample storage grows
  after 100,000 operations; its allocation is the same on both revisions.
- `*-digest.*`: identical ordered outputs across 13 modes and both engines.
- `profile-*`: paired million-operation diagnostics, including setup. These
  profile measurements are not interchangeable with unprofiled primary runs.
- `api-surface.txt`: normal-build go doc listing; no test-only legacy bridges.
- `coverage.txt`, `diff-coverage.json`, `diff-coverage.py`, `lint.txt`, `race.txt`:
  task verification. Diff coverage compares changed production blocks with
  acaa3db, includes runnable examples and excludes test-only helpers.

All performance binaries used Go 1.27.3, darwin/arm64 Apple M1 Max,
`GOMAXPROCS=1`. Run each reproducer alone, without simultaneous tests or lint.
The report records the full baseline commit and dump hash. No data dump is
committed; set `RULEIX_PRODUCTION_DUMP` to its local path when reproducing.
