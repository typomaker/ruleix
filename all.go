package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Scanning up to eight candidate IDs avoids materializing every child result.
// Benchmarks across dense and sparse postings with 2, 4, and 8 children show
// bitmap intersection winning above this shared limit; see
// BenchmarkAllExecutionThreshold.
const allCandidateScanLimit = 8

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

type allRule[T any] struct {
	children             []Rule[T]
	planningProviders    []planningBitmapProvider[T]
	executionDescriptors []ruleExecutionDescriptor
	// sharedWildcardGroups is allocated only when Build finds equality children
	// whose interned, non-empty wildcard bitmap is identical. Ordinary All
	// searches therefore do not pay for duplicate-result tracking.
	sharedWildcardGroups       []int
	duplicateBitmapIDs         map[*roaring.Bitmap]uint32
	duplicateEqualityProviders []equalityResultComponents[T]
	// Tests may search an internal rule before prepareSearch. Runtime indexes
	// set this flag and can distinguish an exact schema from an unprepared one.
	planningPrepared bool
}

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
	for i, child := range r.children {
		if r.executionDescriptors == nil {
			r.executionDescriptors = make([]ruleExecutionDescriptor, len(r.children))
		}
		r.executionDescriptors[i] = describeRuleExecution(child)
		provider := resolvePlanningBitmapProvider(child)
		if provider == nil {
			continue
		}
		if r.planningProviders == nil {
			r.planningProviders = make([]planningBitmapProvider[T], len(r.children))
		}
		r.planningProviders[i] = provider
	}
	r.prepareSharedWildcardGroups()
	r.prepareDuplicateEqualityResults()
	r.planningPrepared = true
}

func (r *allRule[T]) executionDescriptor(index int, child Rule[T]) ruleExecutionDescriptor {
	if len(r.executionDescriptors) == len(r.children) {
		return r.executionDescriptors[index]
	}
	return describeRuleExecution(child)
}

func (r *allRule[T]) prepareSharedWildcardGroups() {
	if r.sharedWildcardGroups != nil {
		return
	}
	firstByWildcard := make(map[*roaring.Bitmap]int)
	nextGroup := 1
	for i, child := range r.children {
		equality, ok := sharedWildcardOf(child)
		if !ok || equality.sharedWildcard().IsEmpty() {
			continue
		}
		wildcard := equality.sharedWildcard()
		first, found := firstByWildcard[wildcard]
		if !found {
			firstByWildcard[wildcard] = i
			continue
		}
		if r.sharedWildcardGroups == nil {
			r.sharedWildcardGroups = make([]int, len(r.children))
		}
		group := r.sharedWildcardGroups[first]
		if group == 0 {
			group = nextGroup
			nextGroup++
			r.sharedWildcardGroups[first] = group
		}
		r.sharedWildcardGroups[i] = group
	}
}

func (r *allRule[T]) prepareDuplicateEqualityResults() {
	owners := make(map[*roaring.Bitmap]int)
	providers := make([]equalityResultComponents[T], len(r.children))
	nextID := uint32(1)
	for i, child := range r.children {
		provider := resolveEqualityResultComponents(child)
		if provider == nil {
			continue
		}
		providers[i] = provider
		provider.visitEqualityResultBitmaps(func(bits *roaring.Bitmap) {
			if id := r.duplicateBitmapIDs[bits]; id != 0 {
				r.duplicateEqualityProviders[i] = provider
				return
			}
			if owner, found := owners[bits]; found && owner != i {
				if r.duplicateBitmapIDs == nil {
					r.duplicateBitmapIDs = make(map[*roaring.Bitmap]uint32)
					r.duplicateEqualityProviders = make([]equalityResultComponents[T], len(r.children))
				}
				r.duplicateBitmapIDs[bits] = nextID
				nextID++
				r.duplicateEqualityProviders[owner] = providers[owner]
				r.duplicateEqualityProviders[i] = provider
				return
			}
			owners[bits] = i
		})
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
		if nested, ok := optimized.(*allRule[T]); ok {
			children = append(children, nested.children...)
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
func (r *allRule[T]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	if pool.local == nil {
		return 0, false
	}
	estimate := ^uint64(0)
	usedCache := false
	for _, child := range r.children {
		childEstimate, cached := cachedCardinality(child, v, pool)
		if !cached {
			estimator, ok := child.(cardinalityEstimator[T])
			if !ok {
				continue
			}
			childEstimate = estimator.estimateCardinality(v)
		} else {
			usedCache = true
		}
		if childEstimate == 0 {
			return 0, usedCache
		}
		estimate = min(estimate, childEstimate)
	}
	return estimate, usedCache
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
	if r.duplicateBitmapIDs != nil {
		rankedChildren = r.deduplicateEqualityResults(v, rankedChildren)
	}
	if !r.shouldValidateCandidates(rankedChildren) {
		if !r.intersectRankedInOrderObserved(v, dst, pool, rankedChildren, metrics) {
			r.releaseRanked(pool, rankedChildren)
			return
		}
		r.releaseRanked(pool, rankedChildren)
		return
	}
	first := rankedChildren[0].bits
	owned := false
	if first == nil {
		first = pool.get()
		r.children[rankedChildren[0].childIdx].search(v, first, pool)
		owned = true
	}
	first.Iterate(func(id uint32) bool {
		for _, ranked := range rankedChildren[1:] {
			if ranked.bits != nil {
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
	if owned {
		pool.put(first)
	}
}

func matchesRuleID[T any](rule Rule[T], value T, id uint32, pool *bitmapPool) bool {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		pool.inspectorObserver(observed.metrics).candidateCheck()
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

//nolint:gocognit // Ranking intentionally keeps the hot-path decisions in one pass.
func (r *allRule[T]) rankChildren(
	v T,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	if pool.local != nil {
		if result, reused := r.reuseLocalPlan(v, pool, rankedChildren); reused {
			return result
		}
	}
	// Rank constant-time equality bounds first. If one is already small enough
	// for direct ID validation, ordered cardinalities cannot improve the chosen
	// execution mode and would only repeat their boundary and posting scans.
	cheapBound := ^uint64(0)
	for i, child := range r.children {
		estimate := ^uint64(0)
		var planningBits *roaring.Bitmap
		var planningFound bool
		if len(r.planningProviders) != 0 || !r.planningPrepared {
			planningBits, planningFound = r.lookupPlanningBitmap(i, child, v)
		}
		if planningFound {
			bits := planningBits
			estimate = bits.GetCardinality()
			rankedChildren[i].bits = bits
			if estimate == 0 {
				return false
			}
			cheapBound = min(cheapBound, estimate)
		} else if cheapEstimate, ok := cheapCardinality(child, v); ok {
			estimate = cheapEstimate
			if estimate == 0 {
				return false
			}
			cheapBound = min(cheapBound, estimate)
		} else if cheapCardinalityIsZero(child, v) {
			return false
		}
		rankedChildren[i].card = estimate
		rankedChildren[i].childIdx = i
	}
	//nolint:nestif // Cache-aware ranking keeps the hot path allocation-free.
	if cheapBound > allCandidateScanLimit {
		for i, child := range r.children {
			if rankedChildren[i].card != ^uint64(0) {
				continue
			}
			if provider, ok := child.(cachedBitmapProvider[T]); ok {
				if bits, found := provider.lookupCachedBitmap(v, pool); found {
					rankedChildren[i].bits = bits
					rankedChildren[i].card = bits.GetCardinality()
					if rankedChildren[i].card == 0 {
						return false
					}
					continue
				}
			}
			if estimate, ok := estimateCardinalityForPlan(child, v, pool); ok {
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
	if pool.local != nil {
		r.rememberLocalPlan(pool, rankedChildren)
	}
	return true
}

//nolint:nestif // The exact fast path keeps cached bitmap checks inline.
func (r *allRule[T]) reuseLocalPlan(
	v T,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) (result, reused bool) {
	plan := pool.allPlans[r]
	if plan == nil || len(plan.order) != len(r.children) {
		return false, false
	}
	if len(r.planningProviders) == 0 && r.planningPrepared {
		for rank, childIdx := range plan.order {
			ranked := rankedBitmap{card: ^uint64(0), childIdx: childIdx}
			child := r.children[childIdx]
			if provider, ok := child.(cachedBitmapProvider[T]); ok {
				if bits, found := provider.lookupCachedBitmap(v, pool); found {
					ranked.bits = bits
					ranked.card = bits.GetCardinality()
					if ranked.card == 0 {
						return false, true
					}
				}
			}
			rankedChildren[rank] = ranked
		}
	} else if !r.populatePlanningLocalPlan(v, pool, plan, rankedChildren) {
		return false, true
	}
	firstCard := rankedChildren[0].card
	if firstCard == ^uint64(0) {
		estimate, ok := estimateCardinalityForPlan(r.children[rankedChildren[0].childIdx], v, pool)
		if !ok || estimate == 0 {
			return estimate != 0, ok
		}
		firstCard = estimate
		rankedChildren[0].card = estimate
	}
	if localPlanCardinalityChanged(plan.firstCard, firstCard) {
		return false, false
	}
	if cachedChildMoreSelective(firstCard, rankedChildren[1:]) {
		return false, false
	}
	return true, true
}

func (r *allRule[T]) populatePlanningLocalPlan(
	v T,
	pool *bitmapPool,
	plan *localAllPlan,
	rankedChildren []rankedBitmap,
) bool {
	for rank, childIdx := range plan.order {
		ranked := rankedBitmap{card: ^uint64(0), childIdx: childIdx}
		bits, found := r.lookupPlanningBitmap(childIdx, r.children[childIdx], v)
		if found {
			ranked.bits = bits
			ranked.card = bits.GetCardinality()
		} else {
			ranked = r.cachedLocalPlanChild(v, pool, childIdx)
		}
		if ranked.card == 0 {
			return false
		}
		rankedChildren[rank] = ranked
	}
	return true
}

func (r *allRule[T]) cachedLocalPlanChild(v T, pool *bitmapPool, childIdx int) rankedBitmap {
	ranked := rankedBitmap{card: ^uint64(0), childIdx: childIdx}
	provider, ok := r.children[childIdx].(cachedBitmapProvider[T])
	if !ok {
		return ranked
	}
	bits, found := provider.lookupCachedBitmap(v, pool)
	if !found {
		return ranked
	}
	ranked.bits = bits
	ranked.card = bits.GetCardinality()
	return ranked
}

func localPlanCardinalityChanged(previous, current uint64) bool {
	if (previous <= allCandidateScanLimit) != (current <= allCandidateScanLimit) {
		return true
	}
	return current > previous*2 || previous > current*2
}

func cachedChildMoreSelective(first uint64, children []rankedBitmap) bool {
	for _, child := range children {
		if child.card != ^uint64(0) && child.card <= first/2 {
			return true
		}
	}
	return false
}

func (r *allRule[T]) rememberLocalPlan(pool *bitmapPool, rankedChildren []rankedBitmap) {
	if pool.allPlans == nil {
		pool.allPlans = make(map[any]*localAllPlan)
	}
	plan := pool.allPlans[r]
	if plan == nil {
		plan = &localAllPlan{order: make([]int, len(rankedChildren))}
		pool.allPlans[r] = plan
	}
	for i, ranked := range rankedChildren {
		plan.order[i] = ranked.childIdx
	}
	plan.firstCard = rankedChildren[0].card
}

func (r *allRule[T]) loadLocalResult(
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	dst *roaring.Bitmap,
) bool {
	if pool.local == nil || pool.observeRuntime {
		return false
	}
	plan := pool.allPlans[r]
	if plan == nil {
		return false
	}
	for i := range plan.results {
		result := &plan.results[i]
		if result.bits == nil || result.epoch != pool.cacheEpoch || len(result.inputs) != len(rankedChildren) {
			continue
		}
		match := true
		for child := range rankedChildren {
			if rankedChildren[child].bits == nil || result.inputs[child] != rankedChildren[child].bits {
				match = false
				break
			}
		}
		if match {
			dst.Or(result.bits)
			return true
		}
	}
	return false
}

func (r *allRule[T]) storeLocalResult(
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	bits *roaring.Bitmap,
) {
	if pool.local == nil || pool.observeRuntime {
		return
	}
	for _, child := range rankedChildren {
		if child.bits == nil || child.owned {
			return
		}
	}
	plan := pool.allPlans[r]
	if plan == nil {
		return
	}
	entry := &plan.results[plan.next]
	plan.next = (plan.next + 1) % uint8(len(plan.results))
	if cap(entry.inputs) < len(rankedChildren) {
		entry.inputs = make([]*roaring.Bitmap, len(rankedChildren))
	} else {
		entry.inputs = entry.inputs[:len(rankedChildren)]
	}
	for i := range rankedChildren {
		entry.inputs[i] = rankedChildren[i].bits
	}
	if entry.bits == nil {
		entry.bits = pool.get()
	} else {
		entry.bits.Clear()
	}
	entry.bits.Or(bits)
	entry.epoch = pool.cacheEpoch
}

func cachedCardinality[T any](rule Rule[T], value T, pool *bitmapPool) (uint64, bool) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		return cachedCardinality(observed.child, value, pool)
	}
	estimator, ok := rule.(cachedCardinalityEstimator[T])
	if !ok {
		return 0, false
	}
	return estimator.estimateCachedCardinality(value, pool)
}

func planningBitmap[T any](rule Rule[T], value T) (*roaring.Bitmap, bool) {
	provider := resolvePlanningBitmapProvider(rule)
	if provider == nil {
		return nil, false
	}
	return provider.lookupPlanningBitmap(value)
}

func resolvePlanningBitmapProvider[T any](rule Rule[T]) planningBitmapProvider[T] {
	switch wrapped := rule.(type) {
	case *inspectedRuntimeRule[T]:
		return resolvePlanningBitmapProvider(wrapped.child)
	case *inspectionDetailsRule[T]:
		return resolvePlanningBitmapProvider(wrapped.child)
	case *lossyRule[T]:
		return resolvePlanningBitmapProvider(wrapped.child)
	}
	provider, ok := rule.(planningBitmapProvider[T])
	if !ok {
		return nil
	}
	return provider
}

func (r *allRule[T]) lookupPlanningBitmap(index int, child Rule[T], value T) (*roaring.Bitmap, bool) {
	if len(r.planningProviders) == len(r.children) {
		provider := r.planningProviders[index]
		if provider == nil {
			return nil, false
		}
		return provider.lookupPlanningBitmap(value)
	}
	return planningBitmap(child, value)
}

func estimateCardinalityForPlan[T any](rule Rule[T], value T, pool *bitmapPool) (uint64, bool) {
	if estimate, ok := cachedCardinality(rule, value, pool); ok {
		return estimate, true
	}
	estimator, ok := rule.(cardinalityEstimator[T])
	if !ok {
		return 0, false
	}
	return estimator.estimateCardinality(value), true
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

//nolint:gocognit // The observed path mirrors execution branches to record exact metrics.
func (r *allRule[T]) intersectRankedInOrderObserved(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	metrics *inspectorRuntime,
) bool {
	if pool.local == nil && r.sharedWildcardGroups != nil {
		rankedChildren = r.collectSharedWildcards(v, pool, rankedChildren)
	}
	for i := range rankedChildren {
		bits := rankedChildren[i].bits
		if i > 0 {
			if i == 1 {
				dst.Or(rankedChildren[0].bits)
			}
			if filtered := filterCandidatesThroughRule(r.children[rankedChildren[i].childIdx], v, dst, pool); filtered {
				if dst.IsEmpty() {
					return false
				}
				continue
			}
		}
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
		//nolint:nestif // Candidate fallback deliberately validates all remaining rule forms here.
		if rankedChildren[i].card <= allCandidateScanLimit {
			dst.Clear()
			rankedChildren[i].bits.Iterate(func(id uint32) bool {
				for j, ranked := range rankedChildren {
					if j == i {
						continue
					}
					if ranked.bits != nil {
						observeCandidateCheck(r.children[ranked.childIdx], pool)
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
		if shouldPruneBitmapRanges(pool) && bitmapRangesDisjoint(current, bits) {
			observeRangePruning(metrics, pool)
			dst.Clear()
			for j := range rankedChildren {
				if rankedChildren[j].owned {
					pool.put(rankedChildren[j].bits)
					rankedChildren[j].owned = false
				}
			}
			return false
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
		if rankedChildren[i].bits == nil {
			continue
		}
		dst.And(rankedChildren[i].bits)
		if dst.IsEmpty() {
			return false
		}
	}
	return true
}

func filterCandidatesThroughRule[T any](rule Rule[T], value T, dst *roaring.Bitmap, pool *bitmapPool) bool {
	if pool.local != nil {
		return false
	}
	filter, ok := rule.(candidateFilter[T])
	if !ok {
		return false
	}
	filter.filterCandidates(value, dst, pool)
	return true
}

func observeCandidateCheck[T any](rule Rule[T], pool *bitmapPool) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		pool.inspectorObserver(observed.metrics).candidateCheck()
	}
}

func observeRangePruning(metrics *inspectorRuntime, pool *bitmapPool) {
	if metrics != nil {
		pool.inspectorObserver(metrics).rangePruning()
	}
}

func shouldPruneBitmapRanges(pool *bitmapPool) bool {
	// Local searches can reuse materialized child bitmaps, so probing their
	// extrema on every hot-path search costs more than the materialization that
	// range pruning can avoid. Keep the heuristic for uncached Index searches
	// searches. Inspection reports the strategy that actually ran and must not
	// enable this otherwise-disabled work.
	return pool.local == nil
}

func bitmapRangesDisjoint(first, second *roaring.Bitmap) bool {
	return first.Maximum() < second.Minimum() || second.Maximum() < first.Minimum()
}

// Keep the allocation-sensitive grouping in one pass; splitting it would
// require carrying partially owned bitmaps across helpers.
func (r *allRule[T]) collectSharedWildcards( //nolint:gocognit
	v T,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) []rankedBitmap {
	for i := range rankedChildren {
		childIdx := rankedChildren[i].childIdx
		group := r.sharedWildcardGroups[childIdx]
		if group == 0 || rankedChildren[i].bits != nil {
			continue
		}
		first, ok := sharedWildcardOf(r.children[rankedChildren[i].childIdx])
		if !ok {
			continue
		}
		bits := pool.get()
		first.addConcreteMatches(v, bits)
		for j := i + 1; j < len(rankedChildren); j++ {
			if r.sharedWildcardGroups[rankedChildren[j].childIdx] != group {
				continue
			}
			other, ok := sharedWildcardOf(r.children[rankedChildren[j].childIdx])
			if !ok {
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
			if r.sharedWildcardGroups[rankedChildren[j].childIdx] == group {
				rankedChildren[j].bits = bits
				rankedChildren[j].card = card
			}
		}
		rankedChildren[i].owned = true
	}

	// Every member of a group now points at the already-combined result
	// W union (A1 intersection ... intersection An). Keep it once so the normal
	// intersection path performs no duplicate And or Contains operations.
	write := 0
	for read := range rankedChildren {
		group := r.sharedWildcardGroups[rankedChildren[read].childIdx]
		if group != 0 {
			duplicate := false
			for previous := range write {
				if r.sharedWildcardGroups[rankedChildren[previous].childIdx] == group {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
		}
		rankedChildren[write] = rankedChildren[read]
		write++
	}
	return rankedChildren[:write]
}

func resolveEqualityResultComponents[T any](rule Rule[T]) equalityResultComponents[T] {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		return resolveEqualityResultComponents(observed.child)
	}
	provider, _ := rule.(equalityResultComponents[T])
	return provider
}

type equalityResultKey struct {
	wildcard uint32
	posting  uint32
}

// Keep the inline and overflow paths together so the common case remains
// allocation-free and the fallback can reuse the already compacted prefix.
func (r *allRule[T]) deduplicateEqualityResults( //nolint:gocognit,nestif
	v T,
	rankedChildren []rankedBitmap,
) []rankedBitmap {
	var seen [8]equalityResultKey
	seenCount := 0
	write := 0
	for read := range rankedChildren {
		duplicate := false
		childIdx := rankedChildren[read].childIdx
		provider := r.duplicateEqualityProviders[childIdx]
		// The nested inline/overflow split keeps the usual path allocation-free.
		//nolint:nestif
		if provider != nil {
			wildcard, posting, deduplicable := provider.lookupEqualityResultComponents(v)
			wildcardID := r.duplicateBitmapIDs[wildcard]
			postingID := r.duplicateBitmapIDs[posting]
			if deduplicable && wildcardID != 0 && (posting == nil || postingID != 0) {
				key := equalityResultKey{wildcard: wildcardID, posting: postingID}
				for previous := range seenCount {
					if seen[previous] == key {
						duplicate = true
						break
					}
				}
				if !duplicate && seenCount < len(seen) {
					seen[seenCount] = key
					seenCount++
				} else if !duplicate {
					// All groups above the inline capacity are rare. Comparing the
					// already-kept children avoids allocating a per-search map.
					for previous := range write {
						previousProvider := r.duplicateEqualityProviders[rankedChildren[previous].childIdx]
						if previousProvider == nil {
							continue
						}
						previousWildcard, previousPosting, ok := previousProvider.lookupEqualityResultComponents(v)
						if ok && r.duplicateBitmapIDs[previousWildcard] == wildcardID &&
							r.duplicateBitmapIDs[previousPosting] == postingID {
							duplicate = true
							break
						}
					}
				}
			}
		}
		if duplicate {
			continue
		}
		rankedChildren[write] = rankedChildren[read]
		write++
	}
	return rankedChildren[:write]
}

func (*allRule[T]) releaseRanked(pool *bitmapPool, rankedChildren []rankedBitmap) {
	for _, child := range rankedChildren {
		if child.bits != nil && child.owned {
			pool.put(child.bits)
		}
	}
}
