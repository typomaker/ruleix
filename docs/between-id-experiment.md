# Direct Between bounds by ID

The baseline is `c12d53641293b7e9c6e241bd39de0a3938af801a`. Both variants
use the same production index, optimizer, and All plan. The test-only
candidate replaces the compiled Between ID matcher in the fixture after
Build. The candidate uses two dense arrays of positions in the existing
ordered blocks. It reads the first physical posting for the lower bound
and the last for the upper bound. The matcher uses the existing wildcard
bitmaps and the original comparator. This preserves independent bound
matches for repeated IDs, including after lossy key merges.

## Verification

`TestBetweenIDExperiment` compares each candidate ID against the original
matcher and checks Index and Local results in Exact and Lossy. The fixture
includes repeated IDs, missing stored and query bounds, and inverted
intervals. The benchmark uses 32,768 unique intervals with 1 or 8 equality
candidates and rotates 1,024 equality keys. The query covers a large share
of both ordered indexes. Build measures the one-candidate distribution,
including construction of the optional table.

Environment: Apple M1 Max, Darwin arm64, Go 1.27.3. Commands:

```sh
go test -run '^TestBetweenIDExperiment$'
go test -c -o /tmp/ruleix-between-final.test
for run in 1 2 3 4 5; do
  /tmp/ruleix-between-final.test -test.run '^$' \
    -test.bench '^BenchmarkBetweenIDExperiment$' -test.benchtime=150ms
done
```

Each invocation runs the baseline and the candidate in the same process
for each case. The Local run reuses one Local instance for the timed loop.
The output slice is cleared between queries and retains its capacity.
Retained table payload is computed from the two arrays and the matcher
object. The rest of the retained index is shared with the baseline.

## Results

Median of five baseline/candidate pairs. The candidate is the prototype
in `between_id_experiment_test.go` in this checkpoint.

| Path | Baseline | Table | B/op, baseline → table | Allocs/op, baseline → table |
| --- | ---: | ---: | ---: | ---: |
| Index, 1 candidate | 4.738 us | 0.204 us | 32 → 32 | 2 → 2 |
| Index, 8 candidates | 75.378 us | 0.510 us | 48 → 48 | 3 → 3 |
| Local, 1 candidate | 5.088 us | 0.163 us | 9 → 0 | 0 → 0 |
| Local, 8 candidates | 77.580 us | 0.403 us | 144 → 0 | 3 → 0 |
| Build, 1 candidate distribution | 52.220 ms | 53.759 ms | 48,374,384 → 48,636,506 | 1,013,779 → 1,013,782 |

The table adds 262,200 bytes per index, about eight bytes per ID. The
initial pointer arrays used 524,344 bytes. Packing positions halves that
overhead, and filling the arrays with Roaring's callback iterator removes
the temporary iterator allocation for each posting.

Separate one-second CPU and heap profiles used the same binary and
`-test.bench '^BenchmarkBetweenIDExperiment$/^Eight$/^Index$/^Baseline$'`
(or `^Table$`) with `-test.cpuprofile` and `-test.memprofile`.
In the baseline, `blockRangeContains` accounted for 79% of sampled CPU
cumulatively. In the candidate, the direct bound checks bypass this path.
The profiled searches both reported 48 B/op and three allocs/op.
`go test ./...` passed.

## Status

The subsequent [compact layout](between-compact-bounds.md) combines the table
with removal of singleton bitmaps; this document records the table-only trial.

The table-only trial measured queries that use the direct ID path. Its
added retained bytes motivated the subsequent compact layout.
