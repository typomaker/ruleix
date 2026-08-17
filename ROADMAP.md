# Roadmap

This document collects possible directions for `ruleix`. Items are exploratory:
their presence here does not promise implementation or inclusion in a specific
release. Candidates should be promoted only after their semantics, indexing
cost, and expected usage are understood.

## High-priority candidates

### Not equal

Add `!=` support to `CompareBy` and consider a discoverable `NotEqual` filter.
Although `Exclude` covers a single forbidden value, comparison-oriented schemas
often expect the conventional set of stored comparison operators to be
complete.

### Set membership

Add `In` and `NotIn` filters for constraints that allow or reject several
values. Common examples include countries, sales channels, customer tiers, and
statuses. Native membership filters could avoid duplicating a stored rule for
each value while making its intent explicit.

Before implementation, define:

- wildcard and empty-set behavior;
- the representation of stored and query values;
- how repeated external IDs interact with multiple allowed or forbidden sets.

## Medium-priority candidates

### Interval overlap

Add `Overlaps` for queries that need any intersection between two intervals:

```text
stored.from <= query.until AND query.from <= stored.until
```

This complements `Between`, which currently requires the stored interval to
fully cover the query interval. Open bounds and inclusive versus exclusive
endpoints need explicit semantics.

### Logical OR

Add an `Any` combinator for logical OR between child rules. This would express
conditions such as `(country = DE OR tier = gold)` without expanding them into
multiple stored rules with the same external ID.

The design must preserve predictable wildcard, exclusion, deduplication, and
cardinality behavior when `Any` is nested inside `All` or another `Any`.

### Presence checks

Consider `Exists` and `Missing` filters for rules that distinguish a present
query field from an absent one. This requires separating presence semantics
from the current use of `nil` as a stored wildcard.

## Domain-specific ideas

These are useful in some workloads but should remain outside the core unless
demand and an efficient indexing strategy are clear:

- string prefix, suffix, substring, glob, or regular-expression matching;
- CIDR and IP address matching;
- collection operators such as `ContainsAny` and `ContainsAll`;
- arbitrary predicates that cannot be indexed ahead of time.

## Suggested evaluation order

1. `!=` and `NotEqual`
2. `In`
3. `NotIn`
4. `Overlaps`
5. `Any`
6. `Exists` and `Missing`

For each candidate, validate real use cases first, document exact matching
semantics, and benchmark build time, search time, and memory use against rule
expansion with the existing filters.
