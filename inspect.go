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

// Inspector observes one rule's latest successfully compiled representation.
// Inspect selects and assigns the implementation when it decorates a rule.
type Inspector interface {
	Bound() bool
	Mode() RuleMode
	Strategy() string
	EntryCount() uint64
	RuleCount() uint64
	MemoryUsage() (uint64, bool)
	MemoryLimit() (uint64, bool)
	ItemCount() (uint64, bool)
	DistinctValueCount() (uint64, bool)
	Granularity() (uint64, bool)
	EstimatedFalsePositiveRate() (float64, bool)
	Searches() uint64
	Materializations() uint64
	CandidateChecks() uint64
	EmptyResults() uint64
	ResultCardinality() ResultCardinalityHistogram
	inspectionState() *inspectorState
}

type inspectorSnapshot interface {
	bound() bool
	mode() RuleMode
	strategy() string
	entryCount() uint64
	ruleCount() uint64
	details() inspectionDetails
}

// inspectionDetails contains optional representation-specific build
// statistics. Availability flags distinguish an unavailable measurement from
// a meaningful zero value.
type inspectionDetails struct {
	MemoryUsageBytes                    uint64
	MemoryUsageAvailable                bool
	MemoryLimitBytes                    uint64
	MemoryLimitAvailable                bool
	Items                               uint64
	ItemsAvailable                      bool
	DistinctValues                      uint64
	DistinctValuesAvailable             bool
	GranularityValue                    uint64
	GranularityAvailable                bool
	EstimatedFalsePositiveRateValue     float64
	EstimatedFalsePositiveRateAvailable bool
}

func representationDetails(memory, items, distinct, granularity uint64, hasGranularity bool) inspectionDetails {
	return inspectionDetails{
		MemoryUsageBytes: memory, MemoryUsageAvailable: true,
		Items: items, ItemsAvailable: true,
		DistinctValues: distinct, DistinctValuesAvailable: true,
		GranularityValue: granularity, GranularityAvailable: hasGranularity,
	}
}

type inspectorSnapshotBox struct{ snapshot inspectorSnapshot }

type inspectorState struct {
	published atomic.Pointer[inspectorSnapshotBox]
	runtime   inspectorRuntime
}

// ResultCardinalityHistogram groups observed materialized results into stable,
// allocation-free buckets. Each bucket includes its lower bound and excludes
// the next bucket's lower bound.
type ResultCardinalityHistogram struct {
	Zero, One, TwoToFour, FiveToSixteen, SeventeenTo256, Above256 uint64
}

type inspectorRuntime struct {
	searches, materializations, candidateChecks, emptyResults atomic.Uint64
	cardinality                                               [6]atomic.Uint64
}

type inspector struct{ state inspectorState }

var _ Inspector = (*inspector)(nil)

func (i *inspector) inspectionState() *inspectorState { return &i.state }

type unboundInspectorSnapshot struct{}

func (unboundInspectorSnapshot) bound() bool                { return false }
func (unboundInspectorSnapshot) mode() RuleMode             { return "" }
func (unboundInspectorSnapshot) strategy() string           { return "" }
func (unboundInspectorSnapshot) entryCount() uint64         { return 0 }
func (unboundInspectorSnapshot) ruleCount() uint64          { return 0 }
func (unboundInspectorSnapshot) details() inspectionDetails { return inspectionDetails{} }

type exactInspectorSnapshot struct {
	strategyName string
	modeName     RuleMode
	entries      uint64
	rules        uint64
	detail       inspectionDetails
}

func (exactInspectorSnapshot) bound() bool { return true }
func (s exactInspectorSnapshot) mode() RuleMode {
	if s.modeName == "" {
		return RuleModeExact
	}
	return s.modeName
}
func (s exactInspectorSnapshot) strategy() string           { return s.strategyName }
func (s exactInspectorSnapshot) entryCount() uint64         { return s.entries }
func (s exactInspectorSnapshot) ruleCount() uint64          { return s.rules }
func (s exactInspectorSnapshot) details() inspectionDetails { return s.detail }

var unboundInspector = &inspectorSnapshotBox{snapshot: unboundInspectorSnapshot{}}

func (i *inspector) snapshot() inspectorSnapshot {
	snapshot := i.state.published.Load()
	if snapshot == nil {
		snapshot = unboundInspector
	}
	return snapshot.snapshot
}

// Bound reports whether a successful build has published a representation.
func (i *inspector) Bound() bool { return i.snapshot().bound() }

// Mode reports the latest representation mode.
func (i *inspector) Mode() RuleMode { return i.snapshot().mode() }

// Strategy reports the latest compiled strategy.
func (i *inspector) Strategy() string { return i.snapshot().strategy() }

// EntryCount reports the number of input entries consumed by the build in the
// latest successful build.
func (i *inspector) EntryCount() uint64 { return i.snapshot().entryCount() }

// RuleCount reports the number of unique external rule IDs in the latest
// successful build.
func (i *inspector) RuleCount() uint64 { return i.snapshot().ruleCount() }

// MemoryUsage reports deterministic accounted representation bytes.
func (i *inspector) MemoryUsage() (uint64, bool) {
	d := i.snapshot().details()
	return d.MemoryUsageBytes, d.MemoryUsageAvailable
}

// MemoryLimit reports the configured representation byte limit.
func (i *inspector) MemoryLimit() (uint64, bool) {
	d := i.snapshot().details()
	return d.MemoryLimitBytes, d.MemoryLimitAvailable
}

// ItemCount reports indexed wildcard and concrete posting items.
func (i *inspector) ItemCount() (uint64, bool) {
	d := i.snapshot().details()
	return d.Items, d.ItemsAvailable
}

// DistinctValueCount reports indexed concrete values, excluding wildcards.
func (i *inspector) DistinctValueCount() (uint64, bool) {
	d := i.snapshot().details()
	return d.DistinctValues, d.DistinctValuesAvailable
}

// Granularity reports the selected number of lossy buckets.
func (i *inspector) Granularity() (uint64, bool) {
	d := i.snapshot().details()
	return d.GranularityValue, d.GranularityAvailable
}

// EstimatedFalsePositiveRate reports an estimate when one is meaningful.
func (i *inspector) EstimatedFalsePositiveRate() (float64, bool) {
	d := i.snapshot().details()
	return d.EstimatedFalsePositiveRateValue, d.EstimatedFalsePositiveRateAvailable
}

func (i *inspector) Searches() uint64         { return i.state.runtime.searches.Load() }
func (i *inspector) Materializations() uint64 { return i.state.runtime.materializations.Load() }
func (i *inspector) CandidateChecks() uint64  { return i.state.runtime.candidateChecks.Load() }
func (i *inspector) EmptyResults() uint64     { return i.state.runtime.emptyResults.Load() }
func (i *inspector) ResultCardinality() ResultCardinalityHistogram {
	b := &i.state.runtime.cardinality
	return ResultCardinalityHistogram{b[0].Load(), b[1].Load(), b[2].Load(), b[3].Load(), b[4].Load(), b[5].Load()}
}

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
	details  inspectionDetails
}

type inspectedRuntimeRule[T any] struct {
	child   Rule[T]
	metrics *inspectorRuntime
}

type inspectedExclusionRule[T any] struct {
	child   exclusionRule[T]
	metrics *inspectorRuntime
}

func (r *inspectedExclusionRule[T]) exclude(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.metrics.searches.Add(1)
	r.metrics.materializations.Add(1)
	before := dst.GetCardinality()
	r.child.exclude(v, dst, p)
	r.metrics.observeCardinality(dst.GetCardinality() - before)
}
func (r *inspectedExclusionRule[T]) isExcluded(v T, id uint32) bool {
	r.metrics.candidateChecks.Add(1)
	return r.child.isExcluded(v, id)
}
func (r *inspectedExclusionRule[T]) hasExclusions() bool { return r.child.hasExclusions() }
func (r *inspectedExclusionRule[T]) internBitmaps(interner *bitmapInterner) {
	if child, ok := r.child.(bitmapInternable); ok {
		child.internBitmaps(interner)
	}
}
func (r *inspectedExclusionRule[T]) prepareSearch() {
	if child, ok := r.child.(ruleSearchPreparer); ok {
		child.prepareSearch()
	}
}

func (*inspectedRuntimeRule[T]) rule() {}
func (r *inspectedRuntimeRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &inspectedRuntimeRule[T]{child: r.child.newState(ids, hints), metrics: r.metrics}
}
func (r *inspectedRuntimeRule[T]) validate(v T) error    { return r.child.validate(v) }
func (r *inspectedRuntimeRule[T]) insert(v T, id uint32) { r.child.insert(v, id) }
func (r *inspectedRuntimeRule[T]) cardinality(v T, p *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, p)
}
func (r *inspectedRuntimeRule[T]) search(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.metrics.searches.Add(1)
	r.metrics.materializations.Add(1)
	before := dst.GetCardinality()
	r.child.search(v, dst, p)
	n := dst.GetCardinality() - before
	r.metrics.observeCardinality(n)
}
func (r *inspectedRuntimeRule[T]) exclude(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.child.exclude(v, dst, p)
}
func (r *inspectedRuntimeRule[T]) collectBuildStatistics(s []nodeBuildStatistics) {
	r.child.collectBuildStatistics(s)
}
func (r *inspectedRuntimeRule[T]) estimateCardinality(v T) uint64 {
	if e, ok := r.child.(cardinalityEstimator[T]); ok {
		return e.estimateCardinality(v)
	}
	return ^uint64(0)
}
func (r *inspectedRuntimeRule[T]) isCardinalityZero(v T) bool {
	if c, ok := r.child.(cardinalityZeroChecker[T]); ok {
		return c.isCardinalityZero(v)
	}
	return false
}
func (m *inspectorRuntime) observeCardinality(n uint64) {
	i := 5
	switch {
	case n == 0:
		i = 0
		m.emptyResults.Add(1)
	case n == 1:
		i = 1
	case n <= 4:
		i = 2
	case n <= 16:
		i = 3
	case n <= 256:
		i = 4
	}
	m.cardinality[i].Add(1)
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
		strategy := inspectionStrategyOf(typed.child)
		mode := inspectionModeOf(typed.child)
		details := inspectionDetailsOf(typed.child)
		child, err := stripInspectors(typed.child, seen, pending)
		if err != nil {
			return nil, err
		}
		*pending = append(*pending, pendingInspection{dst: typed.dst, strategy: strategy, mode: mode, details: details})
		return &inspectedRuntimeRule[T]{child: child, metrics: &typed.dst.runtime}, nil
	case *inspectionDetailsRule[T]:
		return stripInspectors(typed.child, seen, pending)
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
type inspectionDetailer interface{ inspectionDetails() inspectionDetails }

func inspectionDetailsOf[T any](rule Rule[T]) inspectionDetails {
	if details, ok := rule.(inspectionDetailer); ok {
		return details.inspectionDetails()
	}
	return inspectionDetails{}
}

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
