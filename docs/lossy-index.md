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
	ruleix.MaxMemory(20<<20),
)
```

The same policy should eventually apply to `Include`, `Exclude`, `Between`,
ordered comparisons, and `CompareBy`. It should also compose with independently
budgeted children of `All`. Applying one policy to an entire `All` is desirable,
but requires explicit budget-allocation semantics first.

The initial public configuration should contain only `MaxMemory(bytes)`. False
positive rate, hash-function count, bucket count, segment size, prefix length,
and retained bit count are implementation choices. Adding strategies must not
require changing the public API.

Before implementation, specify behavior for non-positive and impossibly small
budgets, unsupported operator/value pairs, overflow, and a representation that
cannot meet the budget without violating correctness. These cases should fail
at build time rather than silently weakening the no-false-negative guarantee.

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
	ruleix.Lossy(ruleix.Include(customerUUID), ruleix.MaxMemory(20<<20)),
	ruleix.Lossy(ruleix.Between(...), ruleix.MaxMemory(10<<20)),
)
```

If every child result is a superset of its exact result, intersecting those
child results remains a superset of the exact conjunction. A policy wrapped
around `All` additionally needs a deterministic budget allocation strategy.
Possible allocation inputs include exact-size estimates, minimum viable sizes,
and expected selectivity; the choice should be driven by memory and search
benchmarks rather than exposed as an initial user setting.

## Diagnostics

Consider build-time statistics containing:

- selected mode (`Exact` or `Lossy`);
- memory used and configured maximum;
- item and distinct-value counts;
- internal strategy and granularity;
- estimated false-positive rate, when meaningful and computable.

False-positive rate is an observable characteristic, not an initial tuning
parameter. Diagnostics must not add work to the default search hot path.

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
