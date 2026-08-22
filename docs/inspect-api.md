# Rule inspection API

## Goal

Ruleix should provide a simple way to identify one `Rule` inside a composite
tree and inspect its runtime state after `Build`. The preferred API name is
`Inspect`: it describes observation and does not suggest that the decorator
changes or grants mutable access to the rule.

`Inspect` must be transparent. Wrapping a rule must not change which stored
constraints match a query, nor should it prevent the planner from replacing
the declarative rule with a different compiled representation.

## API

The initial API is:

```go
type Inspector interface {
	Bound() bool
	Mode() RuleMode
	Strategy() string
	EntryCount() uint64
	RuleCount() uint64
	Reset()
	// unexported method seals implementations
}

func Inspect[T any](dst *Inspector, rule Rule[T]) Rule[T]
```

For example:

```go
var customer ruleix.Inspector

schema := ruleix.All(
	ruleix.Inspect(
		&customer,
		ruleix.Lossy(
			ruleix.Include(customerID),
			ruleix.MaxMemory(20<<20),
		),
	),
)

index, err := ruleix.New[Constraint, string](schema).Build(entries)
if err != nil {
	return err
}

if customer.Bound() {
	mode := customer.Mode()
	strategy := customer.Strategy()
}
```

`Inspector` is deliberately separate from `Rule`. A `Rule` is a
declarative description consumed by the builder; an inspector is a handle to
the runtime state produced from that description. Keeping the types separate
avoids exposing planner internals through the rule-construction API or implying
that callers may mutate a compiled rule. The sealed interface also lets Ruleix
select different internal snapshot implementations for different compiled rule
types without changing the public API.

## Runtime binding

The intended flow is:

```text
Rule -> Inspect(&inspector) -> analyze/plan -> compiled runtime rule
                                                |
                                                v
                                      inspector runtime binding
```

`Inspect` selects an internal implementation and assigns it to the caller's
interface variable immediately. Before `Build`, that inspector is unbound.
During a successful build, it is bound to the runtime representation selected
for that particular decorated rule. The binding must happen after planning so
statistics describe what was actually materialized rather than what the source
rule requested or what the analyzer initially estimated.

Binding is observational only. The compiled rule must have the same search
behavior with or without `Inspect`, and attaching an inspector must not force a
specific strategy, disable optimization, or add work to the default search hot
path.

The first observation method pins one immutable snapshot. All later methods
read that same generation even if another build succeeds. `Reset` releases the
pinned snapshot, and the next method pins the latest successful build. Thus a
caller can read several fields without mixing generations. Observing before
the first successful build pins the unbound state (`Bound() == false`) until
`Reset`.

A failed build does not replace the latest successful state. Observation
methods and `Reset` may run concurrently with searches and a later build (the
builder itself still requires external serialization). One `Inspector` may
identify only one schema location per build. Reusing it at multiple locations
makes `Build` return an error.

## Statistics

The first version exposes methods for the exact representation mode, selected
strategy, number of input entries, and number of unique external rule IDs. The
inspection model leaves room for:

- representation mode: exact or lossy;
- retained memory and configured maximum memory;
- cardinality and item count;
- distinct-value count;
- selected strategy and granularity;
- estimated false-positive rate, when meaningful;
- optimizer decisions and representation details;
- bucket or prefix configuration;
- optional performance counters in a future version.

Fields whose values are unavailable should be distinguishable from meaningful
zero values. Names and units should be stable and explicit: memory is measured
in bytes, false-positive rate is a probability, and estimates should be marked
as estimates. New methods must read the same pinned snapshot as the initial
ones.

Strategy names and fine-grained representation details may evolve as the
planner changes. Decide which values are stable public contracts and which are
diagnostic strings before exposing them.

## Lossy integration

`Inspect` is especially useful around `Lossy` because the final representation
is a build-time decision. After `Build`, an inspector should be able to reveal:

- whether the exact representation fit and was retained;
- which lossy strategy was selected otherwise;
- how much retained memory it uses and what limit was configured;
- which granularity was selected;
- the estimated false-positive rate, when the strategy supports an estimate.

The mechanism remains independent of `Lossy`. The same decorator should work
for exact leaves, composite rules where meaningful, and future runtime
representations without adding inspection methods to every rule type.

## Relationship to diagnostics

`Inspect` observes the built state of a specifically marked rule. A future
`Explain` facility may instead describe planning and execution of a whole tree
or search. They can share internal metadata, but neither API should require the
other. Static build statistics should remain off the search hot path; dynamic
performance counters should be explicitly opt-in if they add runtime cost.

## Validation

Tests should build equivalent schemas with and without `Inspect` and assert
identical search results across leaf rules, nested `All` trees, wildcards, and
lossy representations. Lifecycle tests should cover unbound access, successful
and failed builds, repeated builder use, and concurrent reads under the final
contract. Benchmarks should verify that an unused inspector does not materially
affect build cost, retained memory, search latency, or allocations.
