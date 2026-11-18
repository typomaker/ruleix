# Batch planning evidence

The maintained conclusions are in [batch-planner.md](../../batch-planner.md).
All experiments start from production revision 053c15c; the module/dependencies,
full supplied schema and 96,630-row JSON remain unchanged.

`run.py --phase build` creates isolated binaries with source transformations
from `variants.py` and `lookup.py`. `--variants` selects an explicit comma-
separated list; `--scratch` resumes the printed directory. Metadata points to
per-binary source SHA256 manifests. The common fixture independently verifies
ordered results, append semantics and workload digests before timing starts.
Test-only encode wrappers supply the separate diagnostic counts.

The initial sweep includes Base, LookupAll, Lookup, Order, OrderEmpty, Anchor,
AnchorGuard, Common, Adaptive5, Adaptive8, LookupOrder and LookupCommon.
Mitigations add LookupGuard, LookupHybrid, OrderLarge, CommonReject,
AdaptiveBuild, LookupSelect and LookupWide. Deep controls add native-mask,
ordinary, observed, exclusion and large-value cases. Final native controls
assert their capability; exclusion controls assert the existing fallback.
The `compiled` labels in earlier exploratory Deep controls were not capability
proofs and are not used to establish final native coverage.

The frozen candidate snapshots distinguish iterations; their historical names
do not mean that every candidate was accepted:

- `accepted-initial`: first combined prototype, including its reproduced
  short absent-slot regression and the rotated/partial checks.
- `accepted-wrapper`: early short empty proofs and hash-table gating; the
  default mask path temporarily paid an extra helper call.
- `accepted-fat-map`: direct default loop, retaining dispatch in its large frame.
- `accepted`: small-group bypass candidate, used by Accepted4Deep; lookup reuse remains unselected.
- `accepted-short`: the actual shipped source, used by AcceptedShortDeep.

These snapshots retain unsuccessful mitigations so that reproduction does not
silently replace a measured candidate with the latest source.

The principal timing labels are `sweep` (two reversed 5,000x passes),
`short-a` (short-dispatch screen), `final-core`/`final-short` (three 40,000x
prototype samples), and `working-core`/`working-short`/`working-shapes` (first
production integration). `remedy*` screens track subsequent fixes. Lookup-candidate
measurements use `accepted-*` historical labels; final shipped measurements use
`shipped-*` and AcceptedShortDeep. Means and tails are
medians per run; no percentile is pooled across runs or revisions.

`profile.py` collects paired CPU and allocation data for focused cases. It
accepts `--count` and `--label`; raw pprof files remain in the scratch directory,
while their text summaries are archived. Initial regression profiles cover
ordering on unrelated groups, common-property preprocessing, anchor sampling,
raw-key classification, and rotated/partial sequences. Later profiles cover
the integration regressions and their mitigations. Allocation profiles include
setup; warmed per-request allocation decisions use the unprofiled benchmarks.

`final.py` runs the final pair, with all timings sequential and no concurrent
verification tools. It checks layout, complete and short requests, synthetic
cases, the noisy observed control, Build, retained index bytes and the isolated
Build proof. The shipped proof uses no extra descriptor bytes: layout output
records both index and All sizes. Build/retained probes use fixed 10x counts,
two samples per revision; proof microbenchmarks use 10,000x. Build profiles use
20x. Timing buffers do not grow for normal runs or supported profile counts.

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/full-dump.json
python3 docs/experiments/batch-planner/run.py --phase build \
  --variants BaseFinalDeep,AcceptedShortDeep
python3 docs/experiments/batch-planner/final.py \
  "$(cat /tmp/ruleix-batch-planner-last)" AcceptedShortDeep
python3 docs/experiments/batch-planner/summarize.py \
  "$(cat /tmp/ruleix-batch-planner-last)" accepted-core
python3 docs/experiments/batch-planner/archive.py \
  "$(cat /tmp/ruleix-batch-planner-last)"
```

Run measurement tools alone, after tests/lint finish. The dump is not committed.
`archive.py` splits large summaries into at most 40 cases per JSON file so every
repository artifact remains within 500 lines. Pilot summaries lacking a retained
paired baseline are excluded from maintained evidence and decisions.
