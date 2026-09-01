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

The initial public configuration should contain only `MemoryLimit(bytes)`. False
positive rate, hash-function count, bucket count, segment size, prefix length,
and retained bit count are implementation choices. Adding strategies must not
require changing the public API.

The first implementation will expose this shape:

```go
type LossyOption interface {
	// sealed by an unexported method
}

func Lossy[T any](rule Rule[T], options ...LossyOption) Rule[T]
func MemoryLimit(bytes uint64) LossyOption
```

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

The policy selects the exact representation whenever its planned retained size
fits. Otherwise it selects a supported lossy representation whose planned size
fits and whose result is a superset of the exact result. `Lossy` therefore
never forces approximation and does not promise that every positive budget is
usable.

Build planning is one-pass streaming. At fixed 4096-entry checkpoints, exact
accounting above the private saturating target `MemoryLimit + MemoryLimit/4`
irreversibly compiles the current selective plan. The compiled equality,
numeric ordered, comparator ordered, `Between`, and `CompareBy` leaves accept
subsequent values directly. This replaces the rejected universal-tail
prototype: no additional node or bitmap union is added to published search.
The final accounting pass may coarsen streaming buckets and fails rather than
publish an index above the hard retained `MemoryLimit`. The target bounds
Ruleix-accounted build state opportunistically; neither it nor `MemoryLimit`
is a Go heap or RSS guarantee.

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

## Compiled equality codecs and streaming decision

Equality separates full-value encoding from precision reduction. During
`Build`, direct codecs remain for built-in scalars, `[16]byte`, and
`[2]string`. When the direct type switch misses, reflection inspects the
static type once and compiles named scalars, fixed-byte arrays, recursive
arrays, and structs from verified element sizes and field offsets. Fixed-byte
paths specialize 8/16/20/24/32-byte widths and mix every byte; structs never
hash padding. Complex zero values are canonicalized component-wise; pointers
and channels use identity. `time.Time` is handled as its comparable fields,
including location identity. Interfaces are rejected because their dynamic
type would require search-time inspection. The published leaf retains only a
typed full-hash function and immutable bucket count; search retains no
`reflect.Value`. Equality precision has four levels per power-of-two interval:
`8/8`, `7/8`, `6/8`, and `5/8` of its upper bound. Multiply-high reduction
maps the full hash to non-power-of-two counts without modulo bias. Planning
uses actual accounted Roaring and logical map-slot bytes and discards levels
that release no additional retained memory.

The scalar fixture requires selective `lossy-grouped-hash` behavior, exact-
result superset semantics, deterministic repeated/shuffled planning, and zero
warm `Local.Search` allocations for ordinary and named scalar types.
Unsupported dynamic composites return an internal typed `equalityCodecError`
from `Build` instead of silently selecting a complete-leaf bitmap.

The companion 32,768-entry named-int64 fixture makes exact-first working state
materially exceed the active 125% pressure target. This is deterministic Ruleix
accounting, not a claim about Go heap or RSS. Reproduce the fixtures with:

```sh
go test -run 'TestLossyCodec(ScalarFixtures|UnsupportedCompositeErrors|FixtureBuildOrderAndWorkingPressure)$' -v
```

Apple M1 Max, Go 1.26.0, 10,000 entries, `MemoryLimit(200000)`, 500ms x5:
built-in `int64` measured 185.0 ns/op and named `int64` 185.3 ns/op; both reported
0 B/op and 0 allocs/op. Reproduce with `go test -run '^$' -bench
'^BenchmarkLossyCompiledScalarCodec$' -benchmem -benchtime=500ms -count=5 .`.
Escape-analysis inspection uses `go test -gcflags='all=-m=2' -run
'^TestLossyCodecScalarFixtures$' .`.

Apple M1 Max, Go 1.26.0, 10,000 entries, `MemoryLimit(200000)`, 500ms x5:
ordinary `[16]byte` measured 51.88–53.33 ns/op, named UUID 59.75–61.80,
string 46.71–51.59, `[3]int` 53.84–54.37, and a representative struct
42.51–55.52; all reported 0 B/op and 0 allocs/op. The 10,000-value UUID
distribution fixture requires more than 8,000 occupied high-16-bit buckets for
both ordinary and named forms and verifies every byte changes the hash.
Reproduce with `go test -run '^$' -bench
'^BenchmarkLossyCompiledCompositeCodec$' -benchmem -benchtime=500ms -count=5 .`.
Go 1.26 escape diagnostics conservatively report the temporary generic value
passed to the compiled unsafe plan as moved to heap; the inlined production
call sites nevertheless report 0 B/op and 0 allocs/op in every warm benchmark.
This compiler diagnostic remains an audited caveat rather than a claim that
the closure itself is proven non-escaping.

Equality codecs produce a full `uint64` hash. Precision remains a build-time
representation choice and is applied afterward. In particular, UUID hashing
mixes all 16 bytes; it never truncates one 64-bit half. Finer planned levels use
arbitrary bucket counts and multiply-high reduction rather than only power-of-
two prefixes. Search therefore performs a compiled hash, one immutable bucket
reduction, lookup, and bitmap operation without recalculating precision or
allocating. Types for which no safe codec exists report a codec error instead
of silently losing all equality selectivity.

Aggregate planning keeps its accepted selector. Every leaf starts exact; while
the total exceeds the hard retained limit, the planner chooses the next step
that releases the most bytes, then the larger current leaf, then schema order.
The same large leaf may take consecutive finer downgrade steps while other
leaves remain exact. Planning stops immediately when the total fits.

The first streaming prototype used a universal tail and was rejected after it
produced complete candidate sets, poor scaling, and ordered fit failures. The
accepted design instead mutates the same operator-specific representation that
will be published. On the 10K four-equality benchmark it retained 9.797
candidates/query and zero observed false positives in both ordered and shuffled
input, instead of the prototype's 10,000 candidates and 1.0 false-positive
rate. Build time and transient allocations may be higher; search behavior and
the absence of full exact materialization take priority.

The detailed dependency order and acceptance gates are maintained in
[`ROADMAP.md`](../ROADMAP.md).

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

Start with one equality shape and one ordered-range shape before broad operator
coverage. For every supported combination, compare exact and lossy results over
generated and adversarial data and assert that every exact match appears in the
lossy result. Include wildcards, duplicate external IDs, empty data, skewed
distributions, minimum budgets, range boundaries, and repeated builder use.

Benchmarks should measure 10K, 100K, and 1M rules initially, then larger data
sets where practical. Record analysis and materialization time, peak build
memory, retained index bytes, search latency and allocations, and observed
false-positive rate. A strategy is ready only when it respects the configured
budget within a documented accounting tolerance, preserves immutable lock-free
search, and offers a useful memory tradeoff without an unacceptable search
regression.
