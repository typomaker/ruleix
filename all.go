package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Scanning up to four candidate IDs avoids materializing every child result.
// Benchmarks across dense and sparse postings with 2, 4, and 8 children show
// bitmap intersection winning above this shared limit; see
// BenchmarkAllExecutionThreshold.
const allCandidateScanLimit = 4

// Direct exclusion lookups include a getter and a map lookup per exclusion,
// so they stop paying off sooner than ordinary posting-list membership tests.
const allDirectExclusionScanLimit = 16

// All combines rules with logical AND: a stored constraint matches only when
// every child rule matches. All may be nested.
//
// For example, to match both country and customer tier:
//
//	ruleix.All(
//		ruleix.Include(func(c Constraint) (string, bool) { return c.Country, true }),
//		ruleix.Include(func(c Constraint) (string, bool) { return c.Tier, true }),
//	)
func All[T any](rules ...Rule[T]) Rule[T] { return &allRule[T]{children: rules} }

type allRule[T any] struct{ children []Rule[T] }

func (*allRule[T]) rule()                      {}
func (*allRule[T]) inspectionStrategy() string { return "all" }
func (r *allRule[T]) inspectionMode() RuleMode {
	for _, child := range r.children {
		if inspectionModeOf(child) == RuleModeLossy {
			return RuleModeLossy
		}
	}
	return RuleModeExact
}
func (r *allRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	children := make([]Rule[T], len(r.children))
	for i, child := range r.children {
		children[i] = child.newState(ids, hints)
	}
	return &allRule[T]{children: children}
}
func (r *allRule[T]) validate(v T) error {
	for _, child := range r.children {
		if err := child.validate(v); err != nil {
			return err
		}
	}
	return nil
}
func (r *allRule[T]) insert(v T, id uint32) {
	for _, child := range r.children {
		child.insert(v, id)
	}
}
func (r *allRule[T]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	for _, child := range r.children {
		child.exclude(v, dst, pool)
	}
}
func (r *allRule[T]) collectBuildStatistics(stats []nodeBuildStatistics) {
	for _, child := range r.children {
		child.collectBuildStatistics(stats)
	}
}
func (r *allRule[T]) prepareSearch() {
	for _, child := range r.children {
		prepareRuleSearch(child)
	}
}
func (r *allRule[T]) optimize(total uint64) Rule[T] {
	if len(r.children) == 0 {
		return r
	}
	children := make([]Rule[T], 0, len(r.children))
	var universal *matchAllRule[T]
	for _, child := range r.children {
		optimized := optimizeRule(child, total)
		if matchAll, ok := optimized.(*matchAllRule[T]); ok {
			universal = matchAll
			continue
		}
		children = append(children, optimized)
	}
	if len(children) == 0 {
		return universal
	}
	if len(children) == 1 {
		return children[0]
	}
	return &allRule[T]{children: children}
}
func (r *allRule[T]) cardinality(v T, pool *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, pool)
}
func (r *allRule[T]) matchesID(v T, id uint32) bool {
	for _, child := range r.children {
		matcher, ok := child.(ruleIDMatcher[T])
		if !ok || !matcher.matchesID(v, id) {
			return false
		}
	}
	return true
}
func (r *allRule[T]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	// Most All groups are small. Keeping their ranking storage on the stack
	// avoids a service allocation without adding shared mutable state.
	var inline [8]rankedBitmap
	if len(r.children) <= len(inline) {
		r.searchRanked(v, dst, pool, inline[:len(r.children)])
		return
	}
	buffer := pool.getRanked(len(r.children))
	r.searchRanked(v, dst, pool, buffer.items)
	pool.putRanked(buffer)
}

func (r *allRule[T]) searchRanked(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) {
	if !r.rankChildren(v, pool, rankedChildren) {
		return
	}
	if len(rankedChildren) == 0 {
		return
	}
	if rankedChildren[0].card > allCandidateScanLimit {
		if !r.collectRankedInOrder(v, pool, rankedChildren) {
			return
		}
		dst.Or(rankedChildren[0].bits)
		for _, child := range rankedChildren[1:] {
			dst.And(child.bits)
			if dst.IsEmpty() {
				break
			}
		}
		r.releaseRanked(pool, rankedChildren)
		return
	}
	first := pool.get()
	r.children[rankedChildren[0].childIdx].search(v, first, pool)
	first.Iterate(func(id uint32) bool {
		for _, ranked := range rankedChildren[1:] {
			if !matchesRuleID(r.children[ranked.childIdx], v, id, pool) {
				return true
			}
		}
		dst.Add(id)
		return true
	})
	pool.put(first)
}

func matchesRuleID[T any](rule Rule[T], value T, id uint32, pool *bitmapPool) bool {
	if matcher, ok := rule.(ruleIDMatcher[T]); ok {
		return matcher.matchesID(value, id)
	}
	bits := pool.get()
	rule.search(value, bits, pool)
	matches := bits.Contains(id)
	pool.put(bits)
	return matches
}

func (r *allRule[T]) rankChildren(
	v T,
	_ *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	for _, child := range r.children {
		if checker, ok := child.(cardinalityZeroChecker[T]); ok && checker.isCardinalityZero(v) {
			return false
		}
	}
	for i, child := range r.children {
		estimate := ^uint64(0)
		if estimator, ok := child.(cardinalityEstimator[T]); ok {
			estimate = estimator.estimateCardinality(v)
		}
		rankedChildren[i] = rankedBitmap{card: estimate, childIdx: i}
	}
	// Filter groups are normally small; insertion sort avoids reflection and a
	// closure allocation while preserving schema order for equal estimates.
	for i := 1; i < len(rankedChildren); i++ {
		for j := i; j > 0 && rankedChildren[j].card < rankedChildren[j-1].card; j-- {
			rankedChildren[j], rankedChildren[j-1] = rankedChildren[j-1], rankedChildren[j]
		}
	}
	return true
}

func (r *allRule[T]) collectRanked(
	v T,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	if !r.rankChildren(v, pool, rankedChildren) {
		return false
	}
	return r.collectRankedInOrder(v, pool, rankedChildren)
}

func (r *allRule[T]) collectRankedInOrder(
	v T,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	if pool.local == nil {
		r.collectSharedWildcards(v, pool, rankedChildren)
	}
	for i := range rankedChildren {
		if rankedChildren[i].bits != nil {
			continue
		}
		bits := pool.get()
		r.children[rankedChildren[i].childIdx].search(v, bits, pool)
		card := bits.GetCardinality()
		if card == 0 {
			pool.put(bits)
			for j := range rankedChildren {
				if rankedChildren[j].owned {
					pool.put(rankedChildren[j].bits)
					rankedChildren[j].owned = false
				}
			}
			return false
		}
		rankedChildren[i].bits = bits
		rankedChildren[i].card = card
		rankedChildren[i].owned = true
	}
	for i := 1; i < len(rankedChildren); i++ {
		for j := i; j > 0 && rankedChildren[j].card < rankedChildren[j-1].card; j-- {
			rankedChildren[j], rankedChildren[j-1] = rankedChildren[j-1], rankedChildren[j]
		}
	}
	return true
}

func (r *allRule[T]) collectSharedWildcards(
	v T,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) {
	for i := range rankedChildren {
		if rankedChildren[i].bits != nil {
			continue
		}
		first, ok := r.children[rankedChildren[i].childIdx].(sharedWildcardEquality[T])
		if !ok || first.sharedWildcard().IsEmpty() {
			continue
		}
		groupSize := 1
		for j := i + 1; j < len(rankedChildren); j++ {
			other, ok := r.children[rankedChildren[j].childIdx].(sharedWildcardEquality[T])
			if ok && other.sharedWildcard() == first.sharedWildcard() {
				groupSize++
			}
		}
		if groupSize < 2 {
			continue
		}
		bits := pool.get()
		first.addConcreteMatches(v, bits)
		for j := i + 1; j < len(rankedChildren); j++ {
			other, ok := r.children[rankedChildren[j].childIdx].(sharedWildcardEquality[T])
			if !ok || other.sharedWildcard() != first.sharedWildcard() {
				continue
			}
			concrete := pool.get()
			other.addConcreteMatches(v, concrete)
			bits.And(concrete)
			pool.put(concrete)
		}
		bits.Or(first.sharedWildcard())
		card := bits.GetCardinality()
		for j := i; j < len(rankedChildren); j++ {
			other, ok := r.children[rankedChildren[j].childIdx].(sharedWildcardEquality[T])
			if ok && other.sharedWildcard() == first.sharedWildcard() {
				rankedChildren[j].bits = bits
				rankedChildren[j].card = card
			}
		}
		rankedChildren[i].owned = true
	}
}

func (*allRule[T]) releaseRanked(pool *bitmapPool, rankedChildren []rankedBitmap) {
	for _, child := range rankedChildren {
		if child.bits != nil && child.owned {
			pool.put(child.bits)
		}
	}
}
