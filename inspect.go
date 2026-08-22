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
	// RuleModeLossy reports a conservative representation that may include
	// false positives but never excludes an exact match.
	RuleModeLossy RuleMode = "lossy"
)

// Inspector observes one rule's compiled representation. Its first observation
// pins a coherent build snapshot until Reset. Inspect selects and assigns the
// implementation when it decorates a rule.
type Inspector interface {
	Bound() bool
	Mode() RuleMode
	Strategy() string
	EntryCount() uint64
	RuleCount() uint64
	Reset()
	inspectionState() *inspectorState
}

type inspectorSnapshot interface {
	bound() bool
	mode() RuleMode
	strategy() string
	entryCount() uint64
	ruleCount() uint64
}

type inspectorSnapshotBox struct{ snapshot inspectorSnapshot }

type inspectorState struct {
	published atomic.Pointer[inspectorSnapshotBox]
	pinned    atomic.Pointer[inspectorSnapshotBox]
}

type inspector struct{ state inspectorState }

var _ Inspector = (*inspector)(nil)

func (i *inspector) inspectionState() *inspectorState { return &i.state }

type unboundInspectorSnapshot struct{}

func (unboundInspectorSnapshot) bound() bool        { return false }
func (unboundInspectorSnapshot) mode() RuleMode     { return "" }
func (unboundInspectorSnapshot) strategy() string   { return "" }
func (unboundInspectorSnapshot) entryCount() uint64 { return 0 }
func (unboundInspectorSnapshot) ruleCount() uint64  { return 0 }

type exactInspectorSnapshot struct {
	strategyName string
	modeName     RuleMode
	entries      uint64
	rules        uint64
}

func (exactInspectorSnapshot) bound() bool { return true }
func (s exactInspectorSnapshot) mode() RuleMode {
	if s.modeName == "" {
		return RuleModeExact
	}
	return s.modeName
}
func (s exactInspectorSnapshot) strategy() string   { return s.strategyName }
func (s exactInspectorSnapshot) entryCount() uint64 { return s.entries }
func (s exactInspectorSnapshot) ruleCount() uint64  { return s.rules }

var unboundInspector = &inspectorSnapshotBox{snapshot: unboundInspectorSnapshot{}}

func (i *inspector) snapshot() inspectorSnapshot {
	for {
		if snapshot := i.state.pinned.Load(); snapshot != nil {
			return snapshot.snapshot
		}
		snapshot := i.state.published.Load()
		if snapshot == nil {
			snapshot = unboundInspector
		}
		if i.state.pinned.CompareAndSwap(nil, snapshot) {
			return snapshot.snapshot
		}
	}
}

// Bound reports whether the pinned snapshot belongs to a successful build.
func (i *inspector) Bound() bool { return i.snapshot().bound() }

// Mode reports the representation mode from the pinned snapshot.
func (i *inspector) Mode() RuleMode { return i.snapshot().mode() }

// Strategy reports the compiled strategy from the pinned snapshot.
func (i *inspector) Strategy() string { return i.snapshot().strategy() }

// EntryCount reports the number of input entries consumed by the build in the
// pinned snapshot.
func (i *inspector) EntryCount() uint64 { return i.snapshot().entryCount() }

// RuleCount reports the number of unique external rule IDs in the pinned
// snapshot.
func (i *inspector) RuleCount() uint64 { return i.snapshot().ruleCount() }

// Reset releases the pinned snapshot. The next observation method pins the
// latest successful build, or the unbound state if no build has succeeded.
func (i *inspector) Reset() { i.state.pinned.Store(nil) }

// Inspect decorates rule with an observational handle. It does not change the
// compiled search tree or matching semantics. Inspect panics for a nil
// inspector or rule. Attaching the same inspector more than once in one schema
// makes Build fail.
func Inspect[T any](dst *Inspector, rule Rule[T]) Rule[T] {
	if dst == nil {
		panic("ruleix: nil rule inspector")
	}
	if rule == nil {
		panic("ruleix: nil inspected rule")
	}
	implementation := newInspectorFor(rule)
	*dst = implementation
	return &inspectRule[T]{dst: implementation.inspectionState(), child: rule}
}

// newInspectorFor is the dispatch point for rule-specific Inspector
// implementations. The exact representation currently shares one
// implementation; future strategies may select specialized implementations.
func newInspectorFor[T any](Rule[T]) Inspector { return &inspector{} }

type inspectRule[T any] struct {
	dst   *inspectorState
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
	dst      *inspectorState
	strategy string
	mode     RuleMode
}

func stripInspectors[T any](
	rule Rule[T],
	seen map[*inspectorState]struct{},
	pending *[]pendingInspection,
) (Rule[T], error) {
	switch typed := rule.(type) {
	case *inspectRule[T]:
		if _, exists := seen[typed.dst]; exists {
			return nil, fmt.Errorf("ruleix: one Inspector cannot inspect multiple rules")
		}
		seen[typed.dst] = struct{}{}
		child, err := stripInspectors(typed.child, seen, pending)
		if err != nil {
			return nil, err
		}
		*pending = append(*pending, pendingInspection{dst: typed.dst, strategy: inspectionStrategyOf(child), mode: inspectionModeOf(child)})
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
type inspectionModer interface{ inspectionMode() RuleMode }

func inspectionModeOf[T any](rule Rule[T]) RuleMode {
	if mode, ok := rule.(inspectionModer); ok {
		return mode.inspectionMode()
	}
	return RuleModeExact
}

func inspectionStrategyOf[T any](rule Rule[T]) string {
	if strategy, ok := rule.(inspectionStrategist); ok {
		return strategy.inspectionStrategy()
	}
	return "custom"
}
