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

The same policy should eventually apply to `Include`, `Exclude`, `Between`,
ordered comparisons, and `CompareBy`. It composes with independently budgeted
children of `All`, and one policy may also bound an entire `All` when every leaf
has a supported representation.

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

`Inspector` will expose the selected representation through:

```go
MemoryUsage() uint64
MemoryLimit() uint64
```

Both values are bytes. `MemoryUsage` is a deterministic Ruleix accounting
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

`Build` fails, without publishing an index or a new inspector snapshot, when:

- exact storage exceeds the budget and no lossy strategy supports that
  operator and value type;
- no supported strategy fits the budget, including its minimum viable
  metadata, without risking a false negative;
- canonical encoding rejects an input value, or an accounting calculation
  overflows;
- the policy is invalid, including a missing, duplicate, or zero memory limit;
- a child of a composite `Lossy(All(...))` is unsupported or cannot fit its
  deterministic share of the composite budget.

The error identifies the decorated operator and the reason, but strategy names
and minimum byte counts are diagnostic rather than stable strings. A failed
rebuild leaves the previous immutable index and latest successful `Inspect`
snapshot unchanged, matching the existing builder lifecycle.

Unsupported does not by itself cause failure when the exact representation
fits: no lossy encoder is needed in that case. This permits callers to apply a
uniform policy while operator-specific strategies are rolled out. Custom rule
implementations cannot occur because `Rule` is sealed.

Independently budgeted children of `All` are valid and each owns its entire
limit. One `Lossy` around `All` owns the combined accounted storage of all leaf
representations; structural `All` overhead remains excluded. Nested `Lossy`
decorators are rejected during `Build`; they do not form multiple tiers or
combine budgets. `Inspect` may wrap `Lossy` or be wrapped by it, and both forms
refer to the selected representation. Other option types, soft limits, and
caller-selected false-positive targets require separate contracts.

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
