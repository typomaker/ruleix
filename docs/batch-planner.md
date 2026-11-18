# Batch planning experiments

## Scope and reproducibility

This study evaluates five batch-planning ideas against
`053c15c87abdfaf86156d5b2b484531dfab3623e` (resolved full baseline hash in the experiment metadata). Its production
search implementation is the batch-only API introduced at 8ec5e05. All variants
retain the same public API, per-offer append behavior, first-insertion ID order,
one context per request and portable Go implementation. No field names or
customer-specific recognition appear in the execution policy.

The runner archives baseline production files, applies isolated source
transformations, and builds independent normal-Search binaries. There are no
runtime experiment flags in the measured public API. Common fixtures are based
on [the direct v0.8.2 comparison](production-v082-batch-comparison.md), with
additional unrelated, partial-customer, rotated and identical-offer controls.
Every binary verifies ordered results against the independent scalar oracle,
append preservation, query/result digests, and synthetic matching controls.

All production measurements use the full supplied 96,630-row dump, Go 1.27.3,
Apple M1 Max, darwin/arm64, macOS 26.6.2 and GOMAXPROCS=1. Dump SHA256 is
`ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.
Short controls cover 1/4/5/6/8/10/12/15/16 conditions. Standard requests preserve
one customer and 1–3 neighboring store/slot changes; unrelated/partial/rotated/
identical inputs are explicit stress controls, not substitutes for that model.

Warm-up covers two complete output-buffer cycles. Caller outputs are separate
per condition and reused. Context acquisition/release, analysis and appends are
timed. Mean is Go ns/op including shared clock/reset instrumentation; p99.9
uses nearest-rank per-call samples. Screens use two reversed 5,000-operation
passes, and cannot establish a new tail-performance claim. Confirmations use
three interleaved 40,000-operation samples per revision. Our test/lint/build jobs run separately from timing and profiling. The
developer host is not isolated from unrelated desktop/container work; noisy
controls are repeated with focused interleaving before attribution.

## Ideas and mitigation sequence

| Idea | Initial implementation | Mitigations and investigated limits |
| --- | --- | --- |
| Repeated-value lookups | Compare typed optional getter values before map lookup instead of resolving every live offer. | Full raw-value classification, consecutive reuse, group-size gating, eight-value admission cache, first-pair sampling, and conservative 64-offer gating. Inputs larger than 256 bytes and values larger than 32 bytes keep the original implementation in final guarded variants. |
| Operand order | Rank operands by rejection/cost on first/last sample masks, including ranges. | Try only complete-empty proofs, then limit sampling to groups of at least 16 offers. |
| Anchor selection | Score three offers by capped candidate size and estimated group count. | Keep the original anchor unless its first/last postings differ or its first candidate set is too wide. |
| Common intersections | Detect common typed getter values, compute their joint mask, compact candidate IDs and skip those operands afterward. | Skip groups shorter than eight offers, combine with lookup reuse, then restrict to full empty proofs and groups of at least 32. |
| Adaptive execution | Lower the 16-offer cutoff to 5 or 8 when the selected first posting is small. | Move eligibility to Build; finally retain and use the exact bounded anchor whose global posting bound was proved. |

### Lookup work

For one diagnostic mixed request, equality-key encodes fell from 113 to 25 with
the initial consecutive-reuse prototype. Identical offers fell from 305 to 8. These encode counts include linear indexes as well as hash tables and are
measured by test-only wrappers installed and removed outside timing, not by an
extra counter branch in the production map-lookup loop.

Full raw-value classification saves lookups on rotated values, but distinct
values add typed comparisons and can duplicate work on physically identical
postings. The eight-value admission mitigation returns to physical pointer
classes after overflow. Unconditional consecutive comparisons also add work
when the order is deliberately scrambled. Profiling and subsequent guards
separate these costs from the savings on repeated values. Thresholds are
measured conservative policies rather than universal optimality claims.

### Ordering and common work

Sampling an operand mask is real matching work. On unrelated offers, repeatedly
preparing masks for small groups raised encode counts from 260 to 626 in the
initial ordering variant. CPU profiles locate this work in trialOrderedMasks,
batchMask and direct candidate checks. Group-size gating removes that cliff,
but preprocessing still duplicates work on ordinary homogeneous groups.

Common-property analysis likewise reads getter values before the existing
executor reads varying values again. Range proofs repeat boundary searches and
candidate membership tests. The reject-only variant cuts scanning but adds
those proofs to ordinary positive groups. These are measured implementation
costs; lower operation counts in a partial counter do not prove lower total work.

### Sampled anchors

The normal workload already has a selective shared anchor. Three-offer sampling
adds exact posting lookups without improving that choice in the measured
cohorts. Diagnostic mixed encodes increase 113 → 125; the guarded version avoids
that cost for homogeneous groups but still analyzes heterogeneous groups. This
is a conclusion for these implementations/workloads, not a proof that anchor
sampling can never help another index distribution.

### A Build proof for short batches

A proof that some exact operand is bounded does not guarantee that a different
operand chosen from the first offer stays bounded for later offers. The final
prepared variant therefore records and actually uses the proved source.

Build considers only exact equality operands without wildcards. It finds a
source whose every posting fits the 64-candidate native mask and chooses the
smallest maximum bound. The source index plus one fits in an existing padding
byte of the All descriptor; zero means unavailable. Unsupported mask shapes and
unbounded/wildcard/Lossy sources cannot enable the short path.

At search time, an eligible 5–15-condition call resolves and groups postings
from that specific source. It avoids speculative anchor selection and a wide
fallback discovered after preparation. Shorter calls and unsupported/observed
contexts keep the existing kernel. This proof is an execution-capacity bound,
not knowledge about buyers or stores.

## Decisions and comparable results

Only the prepared source for short requests is selected. Lookup reuse remains
unselected pending conclusive tail attribution. Sampled operand ordering, sampled anchor selection
and common-property compaction are not selected for this workload. Their
initial and mitigated implementations were measured independently and in
combinations; they are not rejected on a single noisy sample.

Ordering on unrelated offers increased equality-key encodes 260 → 626 and
roughly tripled request time in screening. Limiting it to large groups removes
that cliff, but ordinary mixed groups still pay for masks evaluated before
useful pruning. Common compaction also lost on mixed requests, including when
combined with lookup reuse; reject-only probing reduced the loss but still
repeated range preparation on positive groups. Anchor sampling resolves extra
postings while selecting the same useful source in the supplied distribution.
The detailed paired CPU/allocation profiles and mitigation screens are kept
with the reproducer. These decisions concern the tested implementations and
distributions, not all conceivable implementations of those ideas.

The first integrated short path exposed an absent-slot regression: it decoded
the safe customer-sized candidate posting before reaching the empty slot
operand. The correction proves a first-condition exact-key miss across the
whole short batch before candidate decoding. A missing first offer alone is
insufficient; dedicated tests preserve later matching offers.

Lookup integration also required mitigation. A reduction in encode calls is
not automatically a reduction in hash work: one/three-value indexes use linear
lookup. The last value-reuse candidate requires a hash-backed index, at least 64 offers
in the posting group, inputs at most 256 bytes, values at most 32 bytes and a
repeated first pair. Every later value is still checked; the pair is a policy
hint, not a semantic assumption. Small groups call the original posting loop
directly, avoiding an extra dispatch call. A further attempt to merge dispatch
into the large posting-loop frame did not preserve the guarded variant's gains;
that candidate bypasses the dispatcher explicitly for small groups. Variable sequences and large values
retain the original work. Despite these mitigations and mean improvements,
partial-group tail differences persisted across some focused runs. The source
of that residual p99.9 delta was not established conclusively under the shared
host load, so lookup reuse is not shipped. Its investigation remains open;
removing it from the production diff does not claim that the idea is inherently
slow. The shipped equality and native-mask-loop source is byte-for-byte the baseline source.

| Case / conditions | Mean old → shipped, µs | p99.9 old → shipped, µs | Allocations old → shipped |
| --- | ---: | ---: | ---: |
| mixed/5 | 4.465 → 2.714 | 30.834 → 5.500 | 10 → 0 |
| mixed/8 | 5.391 → 3.134 | 29.041 → 7.125 | 10 → 0 |
| mixed/15 | 7.491 → 3.908 | 24.875 → 10.334 | 10 → 0 |
| zero/5 | 0.572 → 0.331 | 0.708 → 0.417 | 0 → 0 |
| slotzero/5 | 0.627 → 0.478 | 0.750 → 0.583 | 0 → 0 |
| slotzero/15 | 1.137 → 0.757 | 1.334 → 0.875 | 0 → 0 |
| intersectzero/5 | 2.777 → 0.797 | 27.875 → 1.041 | 9 → 0 |
| intersectzero/15 | 4.454 → 1.167 | 29.042 → 1.458 | 9 → 0 |
| unrelated/15 | 30.995 → 10.032 | 95.334 → 25.875 | 80 → 0 |

The shipped change is the prepared short path only. Mixed calls of 5/8/15
conditions improve 39.2%/41.9%/47.8% in the final pair. Every measured short
native cohort reports 0 B and 0 allocations after warm-up. The mixed fallback
previously allocated 783 B/10 allocations; the existing-key empty fallback
allocated 762 B/9 allocations.

The equality-loop production source is unchanged from 053c15c. Across the
13 final 100-condition cohorts, means remain within about 1.4% of baseline.
Mixed is 12.445 → 12.322 µs, p99.9 32.375 → 30.042 µs; unrelated is
60.906 → 61.612 µs and partial groups 25.540 → 25.749 µs. These are control
measurements, not a claimed new 100-offer speedup. Existing warm allocation
classes are preserved. Raw tails for every cohort are retained.

The lookup candidates showed real mean improvements, but partial-group tails
were repeatedly higher in some integrations. Focused 250,000-operation checks
used preallocated timing arrays: missing-customer/store/slot means returned to
parity, while partial p99.9 was 52.458 → 55.500 µs. The residual attribution is
unresolved. All lookup code was removed from the production diff, with frozen
candidates and paired CPU/allocation profiles retained. Other large tail
spikes reversed across runs during concurrent VM/Spotlight activity; no service
SLA or general tail improvement is inferred from the shared developer host.


## Profiles, memory and Build

The final index/header layouts are unchanged: Index is 240 bytes and All is
160 bytes on darwin/arm64. The bounded source is stored in existing padding.
The selected source has a maximum of 20 candidates on the supplied full index.
No query cache or retained posting representation was added.

Interleaved 10x Build probes (two samples/revision) measured mean
151.143 → 152.917 ms, approximately +1.2%; bytes/allocations stayed at about
4.785 MB and 46,368.5/op. The isolated preparation step is 0.075 → 100.594 µs,
with 0 B and 0 allocations: it scans immutable posting cardinalities once at
Build instead of paying speculative short-query planning. That step accounts
for about 0.067% of total Build; the whole-probe difference is larger and is
not attributed entirely to it. Paired CPU/allocation profiles preserve the
remaining construction work for review. This bounded Build-time tradeoff is
selected for the material short-search gain; no search regression is accepted.

Two-GC retained-index probes measured approximately 2.58 MB/index on both
versions (2,583,647 → 2,581,455 B/index in this pair). Allocation-table variance
is visible between samples; descriptor sizes and retained structures did not
grow. The standalone proof has no measured transient allocation.

Profiles are paired with the same workload and count. CPU and allocation tops
include dump decoding, Build and timing-array setup; those frames must not be
attributed to per-request matching. Timing buffers are preallocated for every
profile count used. Diagnostics support the cost explanation; unprofiled
interleaved runs determine performance decisions.

## Verification

Full repository tests and full race tests passed. The final short-only source
also passes the scalar digest/native-capability suites, normal builds, lint and
whitespace checks. Changed production statement coverage is 67/67 (100%);
root aggregate coverage is 92.9%. New tests cover bounded-source selection,
wide later queries, nil/unprepared operands, wildcards, observed fallback,
append preservation, first-offer-only misses, all-offer misses, concurrent
matching/empty calls and independence of published builds. No public search
method or type was added. All changed files stay within 500 lines.

The original value-reuse unit tests remain in the experiment directory rather
than imposing an unshipped optimization on production tests. Raw correctness,
coverage, race and lint evidence are archived with the study.

## Reproduction

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/full-dump.json
python3 docs/experiments/batch-planner/run.py --phase build
python3 docs/experiments/batch-planner/run.py --phase screen \
  --label sweep --scratch "$(cat /tmp/ruleix-batch-planner-last)"
```

The [evidence directory](experiments/batch-planner/README.md) records the exact
variant lists, order, counts, raw results and profile commands used in the
maintained conclusions. Per-variant source manifests pin generated production
and fixture files. Historical exploratory variants remain reproducible without
adding flags or public methods to ordinary builds.
