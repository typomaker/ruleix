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
	Snapshot() InspectorSnapshot
	// unexported method seals implementations
}

type InspectorSnapshot struct {
	// unexported fields
}

func (InspectorSnapshot) Bound() bool
func (InspectorSnapshot) Mode() RuleMode
func (InspectorSnapshot) Strategy() string
func (InspectorSnapshot) EntryCount() uint64
func (InspectorSnapshot) RuleCount() uint64
func (InspectorSnapshot) MemoryUsage() (uint64, bool)
func (InspectorSnapshot) MemoryLimit() (uint64, bool)
func (InspectorSnapshot) ItemCount() (uint64, bool)
func (InspectorSnapshot) DistinctValueCount() (uint64, bool)
func (InspectorSnapshot) Granularity() (uint64, bool)
func (InspectorSnapshot) FalsePositiveRate() (float64, bool)
func (InspectorSnapshot) Search() uint64
func (InspectorSnapshot) CacheHit() uint64
func (InspectorSnapshot) CacheMiss() uint64
func (InspectorSnapshot) CacheAdmission() uint64
func (InspectorSnapshot) CacheEviction() uint64
func (InspectorSnapshot) Materialization() uint64
func (InspectorSnapshot) CandidateCheck() uint64
func (InspectorSnapshot) EmptyResult() uint64
func (InspectorSnapshot) CacheEntry() uint64
func (InspectorSnapshot) CacheCapacity() uint64
func (InspectorSnapshot) ResultCardinality() Histogram

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
			ruleix.MemoryLimit(20<<20),
		),
	),
)

index, err := ruleix.New[Constraint, string](schema).Build(entries)
if err != nil {
	return err
}

snapshot := customer.Snapshot()
if snapshot.Bound() {
	mode := snapshot.Mode()
	strategy := snapshot.Strategy()
	used, measured := snapshot.MemoryUsage()
}
```

Runtime counters in successive snapshots are monotonic for the lifetime of the
inspector. `Search` counts executions that
materialize the inspected rule, except for an inspected top-level `All`, where
it counts completed index searches even though the specialized executor can
append the final result without materializing the composite. `CandidateCheck`
counts direct internal-ID membership checks. `Materialization` and the
cardinality histogram count only bitmap materializations, again except that a
top-level `All` contributes its actual final cardinality to the histogram.
This distinction preserves the selected execution strategy.

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

`Snapshot` reads the latest immutable build snapshot exactly once and copies
the currently observed runtime counters into a returned value. All methods on
that value therefore describe the same build generation and counter sample,
even if another build succeeds or searches continue concurrently. A snapshot
captured before the first successful build is unbound (`Bound() == false`).

A failed build does not replace the latest successful state. Observation
methods may run concurrently with searches and a later build (the
builder itself still requires external serialization). One `Inspector` may
identify only one schema location per build. Reusing it at multiple locations
makes `Build` return an error.

## Statistics

The interface exposes methods for the selected representation mode and
strategy, input-entry and unique external-ID counts, plus optional
representation statistics:

- accounted representation memory and configured maximum memory;
- indexed item and distinct-value counts;
- selected bucket count as the current granularity;
- estimated false-positive rate, when meaningful and available;
- optimizer decisions and representation details;
- bucket or prefix configuration;
- monotonic runtime execution counters and result-cardinality histogram.

Optional methods return `(value, available)`, distinguishing unavailable data
from a meaningful zero. Memory is measured in bytes under the deterministic
accounting model used to enforce `MemoryLimit`; it is not Go heap usage.
`ItemCount` includes wildcard and concrete postings after external-ID
deduplication within each posting. `DistinctValueCount` excludes the wildcard.
`Granularity` is the number of selected lossy buckets and is unavailable for
an exact representation. The current strategies do not publish a false-
positive estimate because they lack a meaningful workload-independent model.
All methods on one `InspectorSnapshot` read its captured values.

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

`Inspect` observes the built state and aggregate runtime behavior of a
specifically marked rule. It deliberately does not explain an individual
query: collecting actual child cardinalities would perform extra work and can
misrepresent the execution strategy being diagnosed. Static build statistics
remain off the search hot path, and runtime counters are enabled only for
explicitly inspected rules.

## Validation

Tests should build equivalent schemas with and without `Inspect` and assert
identical search results across leaf rules, nested `All` trees, wildcards, and
lossy representations. Lifecycle tests should cover unbound access, successful
and failed builds, repeated builder use, and concurrent reads under the final
contract. Benchmarks should verify that an unused inspector does not materially
affect build cost, retained memory, search latency, or allocations.
