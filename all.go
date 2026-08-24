package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Scanning up to four candidate IDs avoids materializing every child result.
// Benchmarks across dense and sparse postings with 2, 4, and 8 children show
// bitmap intersection winning above this shared limit; see
// BenchmarkAllExecutionThreshold.
const allCandidateScanLimit = 4

// Range pruning pays for the first two intersections, where an empty result
// avoids most remaining materializations. If they survive, collecting the rest
// eagerly keeps the common overlap path compact and cache-friendly.
const allSequentialIntersectionLimit = 3

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
func (r *allRule[T]) estimateCardinality(v T) uint64 {
	estimate := ^uint64(0)
	for _, child := range r.children {
		if estimator, ok := child.(cardinalityEstimator[T]); ok {
			childEstimate := estimator.estimateCardinality(v)
			if childEstimate == 0 {
				return 0
			}
			estimate = min(estimate, childEstimate)
		} else if checker, ok := child.(cardinalityZeroChecker[T]); ok && checker.isCardinalityZero(v) {
			return 0
		}
	}
	return estimate
}
func (r *allRule[T]) estimateCheapCardinality(v T) uint64 {
	estimate := ^uint64(0)
	for _, child := range r.children {
		if childEstimate, ok := cheapCardinality(child, v); ok {
			if childEstimate == 0 {
				return 0
			}
			estimate = min(estimate, childEstimate)
		} else if cheapCardinalityIsZero(child, v) {
			return 0
		}
	}
	return estimate
}
func (r *allRule[T]) isCheapCardinalityZero(v T) bool {
	return r.estimateCheapCardinality(v) == 0
}
func (r *allRule[T]) isCardinalityZero(v T) bool {
	for _, child := range r.children {
		if checker, ok := child.(cardinalityZeroChecker[T]); ok && checker.isCardinalityZero(v) {
			return true
		}
	}
	return false
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
	r.searchObserved(v, dst, pool, nil)
}

func (r *allRule[T]) searchObserved(v T, dst *roaring.Bitmap, pool *bitmapPool, metrics *inspectorRuntime) {
	// Most All groups are small. Keeping their ranking storage on the stack
	// avoids a service allocation without adding shared mutable state.
	var inline [8]rankedBitmap
	if len(r.children) <= len(inline) {
		r.searchRanked(v, dst, pool, inline[:len(r.children)], metrics)
		return
	}
	buffer := pool.getRanked(len(r.children))
	r.searchRanked(v, dst, pool, buffer.items, metrics)
	pool.putRanked(buffer)
}

func (r *allRule[T]) searchRanked(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	metrics *inspectorRuntime,
) {
	if !r.rankChildren(v, pool, rankedChildren) {
		return
	}
	if len(rankedChildren) == 0 {
		return
	}
	if rankedChildren[0].card > allCandidateScanLimit {
		if !r.intersectRankedInOrderObserved(v, dst, pool, rankedChildren, metrics) {
			r.releaseRanked(pool, rankedChildren)
			return
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
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		observed.metrics.candidateChecks.Add(1)
		return matchesRuleID(observed.child, value, id, pool)
	}
	if matcher, ok := rule.(ruleIDMatcher[T]); ok {
		return matcher.matchesID(value, id)
	}
	bits := pool.get()
	rule.search(value, bits, pool)
	matches := bits.Contains(id)
	pool.put(bits)
	return matches
}

func sharedWildcardOf[T any](rule Rule[T]) (sharedWildcardEquality[T], bool) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		return sharedWildcardOf(observed.child)
	}
	value, ok := rule.(sharedWildcardEquality[T])
	return value, ok
}

func (r *allRule[T]) rankChildren(
	v T,
	_ *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	// Rank constant-time equality bounds first. If one is already small enough
	// for direct ID validation, ordered cardinalities cannot improve the chosen
	// execution mode and would only repeat their boundary and posting scans.
	cheapBound := ^uint64(0)
	for i, child := range r.children {
		estimate := ^uint64(0)
		if cheapEstimate, ok := cheapCardinality(child, v); ok {
			estimate = cheapEstimate
			if estimate == 0 {
				return false
			}
			cheapBound = min(cheapBound, estimate)
		} else if cheapCardinalityIsZero(child, v) {
			return false
		}
		rankedChildren[i] = rankedBitmap{card: estimate, childIdx: i}
	}
	if cheapBound > allCandidateScanLimit {
		for i, child := range r.children {
			if _, ok := cheapCardinality(child, v); ok {
				continue
			}
			if estimator, ok := child.(cardinalityEstimator[T]); ok {
				estimate := estimator.estimateCardinality(v)
				if estimate == 0 {
					return false
				}
				rankedChildren[i].card = estimate
			} else if checker, ok := child.(cardinalityZeroChecker[T]); ok && checker.isCardinalityZero(v) {
				return false
			}
		}
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

func cheapCardinality[T any](rule Rule[T], value T) (uint64, bool) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		return cheapCardinality(observed.child, value)
	}
	estimator, ok := rule.(cheapCardinalityEstimator[T])
	if !ok {
		return 0, false
	}
	return estimator.estimateCheapCardinality(value), true
}

func cheapCardinalityIsZero[T any](rule Rule[T], value T) bool {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		return cheapCardinalityIsZero(observed.child, value)
	}
	checker, ok := rule.(cheapCardinalityZeroChecker[T])
	return ok && checker.isCheapCardinalityZero(value)
}

func (r *allRule[T]) intersectRankedInOrderObserved(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	metrics *inspectorRuntime,
) bool {
	if pool.local == nil {
		r.collectSharedWildcards(v, pool, rankedChildren)
	}
	for i := range rankedChildren {
		bits := rankedChildren[i].bits
		if bits == nil {
			bits = pool.get()
			r.children[rankedChildren[i].childIdx].search(v, bits, pool)
			card := bits.GetCardinality()
			if card == 0 {
				pool.put(bits)
				dst.Clear()
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
		// Conservative or unavailable estimates can put a genuinely small result
		// on the bitmap path. Once a child has been materialized, reuse its measured
		// cardinality and switch to direct validation instead of materializing every
		// remaining child.
		if rankedChildren[i].card <= allCandidateScanLimit {
			dst.Clear()
			rankedChildren[i].bits.Iterate(func(id uint32) bool {
				for j, ranked := range rankedChildren {
					if j == i {
						continue
					}
					if ranked.bits != nil {
						observeCandidateCheck(r.children[ranked.childIdx])
						if !ranked.bits.Contains(id) {
							return true
						}
						continue
					}
					if !matchesRuleID(r.children[ranked.childIdx], v, id, pool) {
						return true
					}
				}
				dst.Add(id)
				return true
			})
			return !dst.IsEmpty()
		}
		if i >= allSequentialIntersectionLimit {
			continue
		}
		if i == 0 {
			continue
		}
		// Compare each next posting list with the accumulated intersection. Its
		// range can narrow after every And, making later pruning more effective.
		current := dst
		if i == 1 {
			current = rankedChildren[0].bits
		}
		if bitmapRangesDisjoint(current, bits) {
			observeRangePruning(metrics)
			dst.Clear()
			for j := range rankedChildren {
				if rankedChildren[j].owned {
					pool.put(rankedChildren[j].bits)
					rankedChildren[j].owned = false
				}
			}
			return false
		}
		if i == 1 {
			dst.Or(current)
		}
		dst.And(bits)
		if dst.IsEmpty() {
			for j := range rankedChildren {
				if rankedChildren[j].owned {
					pool.put(rankedChildren[j].bits)
					rankedChildren[j].owned = false
				}
			}
			return false
		}
	}
	if len(rankedChildren) == 1 {
		dst.Or(rankedChildren[0].bits)
	}
	for i := allSequentialIntersectionLimit; i < len(rankedChildren); i++ {
		dst.And(rankedChildren[i].bits)
		if dst.IsEmpty() {
			return false
		}
	}
	return true
}

func observeCandidateCheck[T any](rule Rule[T]) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		observed.metrics.candidateChecks.Add(1)
	}
}

func observeRangePruning(metrics *inspectorRuntime) {
	if metrics != nil {
		metrics.rangePrunings.Add(1)
	}
}

func bitmapRangesDisjoint(first, second *roaring.Bitmap) bool {
	return first.Maximum() < second.Minimum() || second.Maximum() < first.Minimum()
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
		first, ok := sharedWildcardOf(r.children[rankedChildren[i].childIdx])
		if !ok || first.sharedWildcard().IsEmpty() {
			continue
		}
		groupSize := 1
		for j := i + 1; j < len(rankedChildren); j++ {
			other, ok := sharedWildcardOf(r.children[rankedChildren[j].childIdx])
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
			other, ok := sharedWildcardOf(r.children[rankedChildren[j].childIdx])
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
			other, ok := sharedWildcardOf(r.children[rankedChildren[j].childIdx])
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
