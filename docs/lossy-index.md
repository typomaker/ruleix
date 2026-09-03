# Lossy index design

## Goal and correctness contract

Ruleix should optionally trade preselection precision for bounded retained
index memory. For the same stored rules and query, let `matches` be the result
of the exact index and `result` the result of a lossy index:

```text
exact: result = matches
lossy: result ⊇ matches
```

A lossy index may return false positives, which a caller can validate with an
exact downstream check. It must never return false negatives.

Lossy is permission to approximate, not a demand to do so. Given a 20 MiB
budget, an estimated 8 MiB exact representation should remain exact; an exact
representation estimated at 300 MiB may be replaced by a lossy representation
that fits approximately within the budget.

### Streaming quantization contract

The active implementation milestone replaces the existing lossy planners and
representation ladders. They are compatibility and correctness references,
not constraints on the replacement design. The normative model is one current
generation per rule: an exact key maps to a bitmap in the same physical index
used by Exact, while the rule stores one current precision level. Exact always
uses level 0. Lossy also starts at level 0 and changes level only at a pressure
checkpoint.

Level 0 is the identity function for every key. For level `N`, insertion and
search both compute `quantize(exactKey, N)`. A rebuild computes the next key
only from the currently stored key. Levels must be nested, so for every exact
key the following equality holds:

```text
quantize(quantize(exactKey, N), N+1) = quantize(exactKey, N+1)
```

Here the left-hand outer operation is the incremental `N -> N+1` transform,
not a request for the discarded exact value. A successful transition replaces
the entire generation and releases every reference to the previous generation.
The number of distinct physical keys cannot increase. Insert, rebuild, and
search use the same level definition; input order cannot change the resulting
key classes.

The mandatory key transformer and its stored level are the only precision
state. Level zero is an explicit identity transform for Exact and for the
initial Lossy generation. The Lossy policy metadata, rather than the physical
rule or its level, supplies `Inspect.Mode`. Posting
containers, indexes, matcher logic, range execution, routing, aggregates, and
Local caches are the Exact implementations and receive only physical keys.
They neither inspect the mode nor select precision. In particular, Lossy must
not introduce a parallel posting layout or search node at any level.

At each fixed checkpoint, accounted usage is compared with the saturating soft
target `MemoryLimit + MemoryLimit/4`. While usage exceeds that target, the
aggregate selector evaluates the complete next generation for every eligible
rule, chooses the rule with the largest deterministic retained-byte release,
performs exactly one whole-generation transition, and repeats. Ties are broken
deterministically by schema order after current usage. The hard `MemoryLimit`
applies to the final retained published generations. Independent transient
memory required to construct a checked replacement is tracked separately and
is not reported as retained usage.

A universal bitmap is allowed only at a rule's terminal level, and only when
no more precise available generation can satisfy the hard limit. It cannot be
produced by local pairwise merges or used as an early fallback. If the sum of
terminal retained generations exceeds the applicable hard limit, `Build`
fails without publishing the candidate generation.

The explicit identity-transformer gate was verified on 2026-09-03 with
`go test ./...`, `go test -race ./...`, and
`go test ./... -coverprofile=/tmp/ruleix-step10.cover`. Changed production
lines reached 86/90 statements (95.6% diff coverage). The gate also checks all
standalone ordered transformer kinds at level zero and confirms that the first
Lossy generation retains the common `orderedRule` type. No benchmarks or
profiles were run.

Contract gate verified on 2026-09-02 with `go test ./...` and focused coverage
from `go test ./... -coverprofile=/tmp/ruleix-step1.cover`: every executable
line added in `lossy_levels.go` was covered (100%). No benchmarks or
performance assertions were run or used for this step.

### Mutable generation state

The build-only `lossyBuildState` owns exactly one mutable posting generation,
its current level, and deterministic retained accounting. Insert and search
keys use the same current-level transformation. A downgrade builds a complete,
independent candidate generation, merges colliding postings, and checks its
accounting before a single assignment publishes it. A failed build leaves the
current level and generation unchanged; a successful build clears every old
map entry so the state retains no bitmap from the prior generation.

Candidate posting bytes are reported separately as transient rebuild memory.
They become the new retained accounting only when publication succeeds; no
transient allocation is included in the published retained total. Immutable
aggregates, routing, and other search metadata remain finalized after the last
streaming downgrade by the existing shared Exact publication path.

The step 2 gate was verified on 2026-09-02 with focused state/rebuild tests and
the full test suite. Diff coverage for changed executable production lines was
measured above 90%. No benchmark was run and no layout decision was made.

### Numeric and time ordered levels

Standalone ordered rules over built-in numeric values use a fixed domain-wide
monotonic key grid. `time.Time` uses comparator-backed nested boundaries:
mapping its full domain through Unix seconds cannot represent terminal outward
sentinels without overflowing `time.Time`'s internal epoch. This keeps late
instants and terminal pressure superset-safe. A future logical minute/hour/day
grid must preserve nesting and define timezone semantics before adoption.

Stored lower bounds round downward and stored upper bounds round upward. Query
keys use the opposite outward edge; this preserves strict as well as inclusive
boundary behavior while the shared ordered index continues to execute the
range lookup. Every next level clears one more key bit and rebuilds the complete
current generation. Consequently an incremental transition produces the same
boundary as direct quantization from the exact value, and no exact keys or
future grids are retained.

The numeric codec is enabled only when the supplied comparator agrees with the
natural monotonic encoding of the collected values. Time and other total orders
use the comparator-backed boundary levels described below. The step 4 gate
covers integer extremes, negative and positive time boundaries, strict and inclusive operators, late values outside the
initial range, repeated rebuilds, full tests, and changed-line coverage. No
benchmark or performance conclusion is part of this step. Verification on
2026-09-02 used `go test ./... -coverprofile=/tmp/ruleix-step4.cover` and
reported 96.2% executable changed-line coverage (175/182), followed by
`git diff --check`.

### Comparator-backed boundary levels

An ordered rule whose value type has no proven scalar codec, or whose supplied
comparator disagrees with that codec, uses boundaries taken from the keys in
the current shared `orderedIndex`. The comparator must define one stable,
transitive total order across every Build and Search value. Ruleix does not try
to recover an order from reflection, a getter, or the equality codec.

Each transition groups adjacent current keys in comparator order. A stored
lower bound uses the first key of its group and a stored upper bound uses the
last; search values use the opposite nearest boundary. Thus every boundary in
level `N+1` is a boundary from level `N`, and the parent is computable without
an exact value. The ordered postings, block aggregates, routing, matcher and
Local cache remain the Exact implementations.

Boundary lookup searches the physical index directly. No parallel boundary
array is retained or omitted from memory accounting. A value inside the known
range maps to the nearest outward boundary. A late value beyond either open
edge remains its own new extreme key, because mapping it inward could create a
false negative; the next pressure transition includes that key in the normal
whole-generation rebuild. Immutable search uses the same transient edge key
without inserting it. Late insertion changes neither the level nor the rest of
the generation; the next parent is computed only from current physical keys.

### Compound ordered levels

`Between` owns two independent ordered generations: the lower (`from`) side
and the upper (`until`) side. A pressure transition selects exactly one side,
rebuilds that complete side through the shared ordered quantizer, and leaves
the other side and its level unchanged. Stored lower bounds round down and
stored upper bounds round up; query bounds use the corresponding opposite
edge through the same fused `Between` matcher and cache path.

`CompareBy` similarly owns one level for each stored operator (`EQ`, `LT`,
`LTE`, `GT`, and `GTE`). Pressure advances one complete operator index at a
time. Range operators use their outward lower/upper direction; equality maps
both inserted and searched values to the same physical bucket. All five
operator indexes and both `Between` sides remain in the common `compareByRule`, `betweenRule`, and `orderedIndex` types. There are no quantized compound runtime
or search wrappers; Exact/Lossy mode comes only from policy metadata. Search, aggregate blocks, routing, candidate filtering, and Local caching remain shared with Exact.

No compatibility planner remains. `Between` and `CompareBy` enter the same single-current-generation streaming state as standalone leaves and derive
only the next complete side/operator generation when pressure requests it.
Late inserts are transformed immediately at the selected per-side or
per-operator level. Missing values, duplicates, strict/inclusive operators,
repeated downgrades and hard accounting are covered by the differential gate.

## Public API direction

Lossy behavior should decorate an existing `Rule[T]`:

```go
ruleix.Lossy(
	ruleix.Include(...),
	ruleix.MemoryLimit(20<<20),
)
```

The same policy applies to `Include`, ordered comparisons, `Between`, and
`CompareBy`. It composes with independently budgeted children of `All`, and one
policy may also bound an entire `All`. `Exclude` remains outside the current
lossy representation set.

The public configuration contains only `MemoryLimit(bytes)`; approximation
quality does not override the hard retained-memory contract.

`LossyOption` is sealed so configuration can grow without accepting arbitrary
third-party implementations. `Lossy` panics when `rule` is nil, consistently
with `New` and `Inspect`. All policy validation is deferred to `Build`:
schema constructors do not return errors, and a caller may prepare one schema
before its input distribution is known.

Exactly one `MemoryLimit` option is required in the initial API. A missing or
repeated option, a zero budget, or an option value that overflows an internal
size calculation makes `Build` fail. `uint64` avoids architecture-dependent
meaning at the API boundary; converting it to `int` is a checked build-time
operation. There is no implicit default and values are bytes, not MiB units.

### Budget contract

`MemoryLimit` is a hard upper bound for accounted memory retained exclusively
by the decorated rule's selected search representation after a successful
`Build`.
It includes its posting containers, value keys or buckets, lookup tables, and
strategy metadata. It excludes the builder's transient analysis state, the
index's external-ID table, bitmap pool state, the rule's getter/comparator,
and structural overhead belonging to an enclosing `All`.

Shared immutable storage is charged once to its owning decorated rule. The
initial implementation must not share budgeted storage across independently
budgeted decorators: doing so would make either inspector's accounting depend
on whether the other decorator exists. Allocator overhead is deliberately not
inferred from the live Go heap. The accounted retained byte count must not
exceed the budget.

### Stable memory accounting

`InspectorSnapshot` exposes the selected representation through:

```go
MemoryUsage() (uint64, bool)
MemoryLimit() (uint64, bool)
```

Both values are bytes. `MemoryLimit` is the maximum available to that subtree
after accounting for its local configuration, every ancestor cap, and the
selected representations of its siblings. `MemoryUsage` is the selected
subtree's deterministic Ruleix accounting
value, not a sample from `runtime.MemStats`, a heap profile, or an estimate
derived from the Go allocator. Given the same input, policy, and selected
strategy, it must be identical across repeated builds, supported architectures,
Go versions, and unrelated process activity.

Every representation must define its accounting formula beside its
implementation. The common rules are:

- canonical key and value encodings contribute their encoded byte lengths;
- Roaring postings contribute their portable serialized sizes;
- fixed strategy metadata and logical table slots use explicitly versioned,
  architecture-independent byte charges;
- logical lengths are counted, while Go object headers, pointer widths, map
  implementation details, slice capacity slack, allocator size classes, and
  garbage-collector metadata are not.

Analysis and planning compute the same formula later reported by
`MemoryUsage`; materialization must verify it before publishing the index. A
strategy or accounting-formula change may change the reported usage and must
be recorded as an observable release change. It does not change what one byte
means or make results dependent on the runtime allocator.

This is a stable representation budget, not a promise that a heap profile will
attribute exactly that many physical bytes to the rule. Peak build memory and
actual process heap remain benchmark metrics reported separately. The explicit
model makes the build decision reproducible and lets callers compare usage to
the configured limit without allocator noise.

The policy retains the exact current generation whenever its accounted size
fits. Otherwise it repeatedly rebuilds one complete leaf generation through
its next nested level until the published state fits. `Lossy` therefore
never forces approximation and does not promise that every positive budget is
usable.

Build planning is one-pass streaming and follows the current-generation
checkpoint algorithm defined above. The final accounting pass fails rather
than publish an index above the hard retained `MemoryLimit`; neither the soft
target nor `MemoryLimit` is a Go heap or RSS guarantee.

`Build` fails, without publishing an index or a new inspector snapshot, when:

- no supported strategy fits the budget, including its minimum viable
  metadata, without risking a false negative;
- canonical encoding rejects an input value, or an accounting calculation
  overflows;
- the policy is invalid, including a missing, duplicate, or zero memory limit;
- the sum of minimum viable child representations cannot fit an applicable
  composite policy budget.

The error identifies the decorated operator and the reason, but strategy names
and minimum byte counts are diagnostic rather than stable strings. A failed
rebuild leaves the previous immutable index and latest successful `Inspect`
snapshot unchanged, matching the existing builder lifecycle.

Every built-in equality and ordered leaf has a conservative terminal
representation. Equality uses bucketed hashing for built-in and named scalars,
fixed-byte arrays such as UUIDs, recursive arrays, comparable structs, complex
values, pointer and channel identity, and `time.Time`. A type for which no safe
allocation-free semantic codec can be compiled fails `Build` with a typed
codec error instead of silently selecting a complete ID set. Ordered comparisons
use numeric order-preserving keys where available and comparator-ordered
buckets otherwise. `Between` uses two comparator-bucket indexes inside one
fused rule. A stored lower bound is rounded toward the bucket minimum and a
stored upper bound toward the bucket maximum, so an approximate interval only
expands. `CompareBy` builds comparator buckets independently for `EQ`, `LT`,
`LTE`, `GT`, and `GTE`, then unions the operator ranges matching the query.
Both terminal levels retain the exact-or-superset contract without the former
complete-leaf universal fallback. Custom rule implementations cannot occur
because `Rule` is sealed.

### Step 8 legacy removal and final correctness gate

The static representation planner and all of its production state have been
removed: there are no leaf ladders, prebuilt future candidates, capacity-driven
pairwise merges, universal-tail wrappers, or separate universal rule type.
Initial policy materialization publishes accounted exact leaves inside the
same adaptive wrapper used by later checkpoints. The hard-limit pass advances
the live generation with the deterministic release selector; a terminal
one-key generation is reached only by those ordinary nested transitions.

`Inspect.Strategy` is always the common physical family. Exact leaves expose
no granularity; lossy leaves report the number of currently published rounded
keys, and `DistinctValueCount` for ordered lossy leaves describes that same
published generation. Memory details and nested effective limits are refreshed
after final fitting. Equality planning refuses to borrow a singleton/small
posting as a bitmap, preventing the empty-candidate false negative exposed by
the aggregate regression fixture. Floating-point ordered rules use comparator
boundary levels so NaN follows the supplied stable total order.

Verification on 2026-09-03 used `go test ./... -count=1`, `go test -race
./... -count=1`, targeted differential/streaming/determinism tests, and `git
diff --check`. Coverage over added or modified executable production lines was
92.7% (140/151). No benchmark or profile result was used as a gate.

No search-time reflection is used. Composite equality accounting reflects over
keys only during `Build` and charges architecture-independent logical scalar,
array, struct, and string sizes. Hashing and ordered boundary lookup on the
published index use specialized functions, Go comparators, and binary search.

Independently budgeted children of `All` are valid and each owns its entire
limit. One `Lossy` around `All` owns the combined accounted storage of all leaf
representations; structural `All` overhead remains excluded. Nested `Lossy`
decorators form a hierarchy of hard caps. Planning applies descendant caps
bottom-up and then lets an ancestor advance any leaf in its subtree to a
coarser discrete representation. An ancestor can therefore force a nested
policy below its configured limit, but cannot relax that limit. Direct nesting
uses the smaller cap regardless of order; sibling policies retain independent
local caps while also contributing to their ancestor's accounted total.
Ordinary nested `All` nodes flatten only within the nearest policy boundary.
If a subtree's minimum viable total exceeds its cap, `Build` fails with the
policy path. `Inspect` boundaries remain attached to their corresponding
subtrees and never report accounted usage above the applicable cap.
Missing, repeated, zero, or otherwise malformed options in a nested policy are
validated before entries are consumed and report the same stable policy path;
tests and callers should rely on that path and error category rather than an
unstable representation byte total.

Aggregate planning begins with every supported leaf exact. If the total does
not fit, it advances one leaf by one discrete representation level and
recomputes the total. The deterministic selector maximizes bytes released,
breaks ties by larger current leaf usage, and finally uses stable schema order.
Selection stops immediately when the aggregate fits, so no unrelated leaf is
downgraded. Ordinary nested `All` nodes participate in the same allocation
pool without losing their search shape or inspection boundary.

Every `Build` plans only from its current input and publishes a new immutable
index. Adding data or changing a schema affects the next build; published
indexes never replan in place, and concurrent `Index.Search` remains lock-free.

## Compiled equality codecs

Equality separates a typed full-value hash from nested precision reduction.
Build may compile codecs for scalar and comparable composite types; interfaces
whose dynamic values would require search-time reflection are rejected. The
published leaf retains only its typed hash function, current quantizer and
physical postings. Detailed performance evidence and rejected prototypes live
in `performance-history.md` and `optimization-decisions.md`.

## Build-time planning

Representation selection belongs in `Build` and has three conceptual phases:

```text
Analyze -> Plan -> Materialize
```

Analysis collects only the data needed to plan, such as item count, distinct
values, distribution, bounds, and estimated exact memory. Planning chooses
exact or lossy mode, a strategy, granularity, and expected memory. Materialize
then constructs the chosen index. The build must not require a complete exact
index merely to discover that it exceeds the budget.

Strategy selection depends on both operator semantics and value type. For
example, equality over strings may use prefix, hash, or Bloom-like grouping,
while numeric ranges may use ordered buckets and time ranges may use temporal
segments. Equality and range rules for the same Go type need not share a
representation.

Select a specialized search function once during `Build`, using an internal
registry or type switch. Search must not repeatedly inspect the dynamic type.

## Canonical encodings

Lossy grouping must be based on the value's semantics, not the first bits of a
Go value's memory. A general `unsafe` encoding is invalid for strings,
`time.Time`, structs, pointer-containing types, and other values whose memory
layout is not a canonical value representation. A local `unsafe` optimization
may be considered only for a specific encoder after benchmarks establish its
value and tests establish identical semantics.

For ordered scalar values, investigate an internal mapping:

```text
OrderedKey(T) uint64
a < b implies OrderedKey(a) < OrderedKey(b)
```

Signed and unsigned integers, floating-point values, and `time.Time` could then
share a bucket or shift-based range index. The design must explicitly cover
integer widths and signs, floating-point negative zero, infinities and NaNs,
and time normalization. Boundary and property tests must verify monotonicity.

## Automatic granularity and strategies

Ruleix should derive prefixes, bit counts, shifts, and segment sizes from the
memory budget, observed data, operator, and value type. Callers should not tune
these implementation details.

Potential experiments include:

| Rule and value shape | Candidate representation |
| --- | --- |
| String equality | canonical prefix, grouped hash, or Bloom-like structure |
| UUID equality | canonical prefix bits |
| Integer equality | bit-prefix or grouped values |
| Numeric range | monotonic-key buckets |
| Time range | monotonic-key or temporal segments |

These are experiments rather than commitments. Any selected representation
must satisfy the result-superset invariant for wildcard, missing-query,
exclusion, open-bound, and comparison-operator semantics as applicable.

## Composition

Lossy is an orthogonal rule policy, so independently budgeted children should
compose normally:

```go
ruleix.All(
	ruleix.Include(region),
	ruleix.Lossy(ruleix.Include(customerUUID), ruleix.MemoryLimit(20<<20)),
	ruleix.Lossy(ruleix.Between(...), ruleix.MemoryLimit(10<<20)),
)
```

If every child result is a superset of its exact result, intersecting those
child results remains a superset of the exact conjunction. A policy wrapped
around `All` first computes every leaf's exact accounted size. If their sum
fits, every leaf remains exact. Otherwise it first reserves each leaf's minimum
viable representation, then divides the remaining bytes in proportion to each
leaf's exact-size headroom. Since representation granularity can leave a share
unused, the allocator reclaims those bytes and applies the smallest affordable
upgrade, breaking ties by schema order, until no child can improve within the
pool. The build fails only when the sum of minimum viable representations
exceeds the limit. Nested `All` groups use the same flattened leaf allocation
while retaining their search structure.

## Diagnostics

Expose build-time statistics through the general-purpose `Inspect` proposal
described in [`inspect-api.md`](inspect-api.md), rather than adding a
lossy-specific handle. Useful fields include:

- selected mode (`Exact` or `Lossy`);
- memory used and configured maximum;
- item and distinct-value counts;
- internal strategy and granularity;
- estimated false-positive rate, when meaningful and computable.

False-positive rate is an observable characteristic, not an initial tuning
parameter. `Inspect` must reveal the representation that planning actually
selected, including an exact representation when it fits the budget.
Diagnostics must not add work to the default search hot path.

Grouped-hash equality reports a build-time collision estimate over concrete
posting items. It is the fraction of ordered pairs belonging to different
stored values that land in the same selected bucket; wildcard items are
excluded because they are exact matches for every present query. The estimate
describes the indexed distribution and does not require queries or runtime
instrumentation. Ordered buckets do not expose one distribution-independent
rate because their false-positive boundary depends on the query value.

## Validation and rollout

Correctness gates compare exact and lossy results on generated and adversarial data; performance gates measure retained memory, candidate quality, latency and allocations. The active requirements and sequencing live in `ROADMAP.md`.
Step 11 verification on 2026-09-03 used `go test ./...`, `go test -race ./...`, `go test ./... -count=5`, and `go test ./... -coverprofile=/tmp/ruleix-step11.cover`; all passed, repository coverage was 90.8%, and the changed production functions reported by `go tool cover -func` were fully covered except the existing `prepareStreamingNext`/limit branches (still above the 90% changed-code gate). No benchmarks or profiles were run.
