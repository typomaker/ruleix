package ruleix

import (
	"fmt"
	"sync/atomic"

	"github.com/RoaringBitmap/roaring/v2"
)

// RuleMode describes whether a compiled rule retains exact matching semantics
// or uses an approximation that may return false positives.
type RuleMode string

const (
	// RuleModeExact reports a representation with no false positives.
	RuleModeExact RuleMode = "exact"
)

// RuleStats is an immutable snapshot of the latest successful build containing
// an inspected rule. A zero value, with Bound false, means that no build has
// successfully bound the inspector yet.
type RuleStats struct {
	Bound      bool
	Mode       RuleMode
	Strategy   string
	EntryCount uint64
	RuleCount  uint64
}

// RuleInspector observes the compiled representation of a rule. Its zero value
// is ready for use. A RuleInspector must not be copied after first use.
//
// Stats may be called concurrently with searches and later (externally
// serialized) builds of the Builder that owns the inspected schema.
type RuleInspector struct {
	stats atomic.Pointer[RuleStats]
}

// Stats returns a snapshot from the latest successful build. Failed builds
// preserve the previous snapshot.
func (i *RuleInspector) Stats() RuleStats {
	if i == nil {
		return RuleStats{}
	}
	stats := i.stats.Load()
	if stats == nil {
		return RuleStats{}
	}
	return *stats
}

// Inspect decorates rule with an observational handle. It does not change the
// compiled search tree or matching semantics. Inspect panics for a nil
// inspector or rule. Attaching the same inspector more than once in one schema
// makes Build fail.
func Inspect[T any](dst *RuleInspector, rule Rule[T]) Rule[T] {
	if dst == nil {
		panic("ruleix: nil rule inspector")
	}
	if rule == nil {
		panic("ruleix: nil inspected rule")
	}
	return &inspectRule[T]{dst: dst, child: rule}
}

type inspectRule[T any] struct {
	dst   *RuleInspector
	child Rule[T]
}

func (*inspectRule[T]) rule() {}
func (r *inspectRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &inspectRule[T]{dst: r.dst, child: r.child.newState(ids, hints)}
}
func (r *inspectRule[T]) validate(v T) error    { return r.child.validate(v) }
func (r *inspectRule[T]) insert(v T, id uint32) { r.child.insert(v, id) }
func (r *inspectRule[T]) cardinality(v T, pool *bitmapPool) uint64 {
	return r.child.cardinality(v, pool)
}
func (r *inspectRule[T]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.search(v, dst, pool)
}
func (r *inspectRule[T]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.exclude(v, dst, pool)
}
func (r *inspectRule[T]) collectBuildStatistics(stats []nodeBuildStatistics) {
	r.child.collectBuildStatistics(stats)
}
func (r *inspectRule[T]) optimize(total uint64) Rule[T] {
	return &inspectRule[T]{dst: r.dst, child: optimizeRule(r.child, total)}
}

type pendingInspection struct {
	dst      *RuleInspector
	strategy string
}

func stripInspectors[T any](
	rule Rule[T],
	seen map[*RuleInspector]struct{},
	pending *[]pendingInspection,
) (Rule[T], error) {
	switch typed := rule.(type) {
	case *inspectRule[T]:
		if _, exists := seen[typed.dst]; exists {
			return nil, fmt.Errorf("ruleix: one RuleInspector cannot inspect multiple rules")
		}
		seen[typed.dst] = struct{}{}
		child, err := stripInspectors(typed.child, seen, pending)
		if err != nil {
			return nil, err
		}
		*pending = append(*pending, pendingInspection{dst: typed.dst, strategy: inspectionStrategyOf(child)})
		return child, nil
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			clean, err := stripInspectors(child, seen, pending)
			if err != nil {
				return nil, err
			}
			children[i] = clean
		}
		return &allRule[T]{children: children}, nil
	default:
		return rule, nil
	}
}

type inspectionStrategist interface{ inspectionStrategy() string }

func inspectionStrategyOf[T any](rule Rule[T]) string {
	if strategy, ok := rule.(inspectionStrategist); ok {
		return strategy.inspectionStrategy()
	}
	return "custom"
}
