# v0.8.2 versus current short-batch API

Maintained report: [comparison](../../production-v082-short-batch-comparison.md).

`run.py` archives v0.8.2 and 9c95f7d, loads their own production dependencies,
adds the same schema/workload/oracle fixtures, verifies 45 ordered digests and
interleaves three 40,000-request samples per revision. Only adapter_test.go
changes between the old Local loop and the current batch Search call.

The common generator, schema, cohort selector and predicate-miss builder are
fetched from pinned repository revisions; they are not reconstructed separately
for each release. `comparison.go.txt` verifies all five request lengths with
scalar expected results and existing destination sentinels. `benchmark.go.txt`
uses identical warm-up, independent per-offer outputs and timing instrumentation.
All generated test files receive the go1.24 constraint for modern benchmark loops
in the old go1.23 module. Go formatting is normalized before hashing/building.

`summarize.py` requires three samples per version and all 45 cases. It writes
one summary JSON per length (nine modes each), keeping each artifact below
500 lines, and prints the full comparison table. Raw measurements preserve the
old/new/new/old/old/new order and contain mean, p99, p99.9, B/op and allocs/op.
The digest reports record 2,205 requests and 63,504 offers per version, with
matching per-mode/length query and ordered-result SHA256 values.

Set RULEIX_PRODUCTION_DUMP to the full local JSON and run the commands in the
report. The runner prints the source/binary scratch directory and records it in
/tmp/ruleix-v082-short-last. No production search code is changed by this study.

Latest local medians: mixed 5/15/100 requests take 8.658/16.773/74.365 µs
on v0.8.2 versus 2.607/3.891/12.401 µs on 9c95f7d (3.32/4.31/6.00x).
All current cases report zero warm allocations; all five misses improve.
The benchmark fixture preserves the exact measured source, including its
inherited planner-study comment; this report and evidence define this run.
