package ruleix

import "github.com/RoaringBitmap/roaring/v2"

func (r *allRule[T]) deduplicateEqualityClasses(
	v T,
	rankedChildren []rankedBitmap,
	checked []uint64,
) []rankedBitmap {
	write := 0
	for read := range rankedChildren {
		provider := resolveEqualityClassProvider(r.children[rankedChildren[read].childIdx])
		class := uint32(0)
		if provider != nil {
			class = provider.lookupEqualityClass(v)
		}
		if class != 0 {
			word, bit := (class-1)/64, uint64(1)<<((class-1)%64)
			if r.executionCounters != nil {
				r.executionCounters.maskTests++
			}
			if checked[word]&bit != 0 {
				if r.executionCounters != nil {
					r.executionCounters.skippedOperands++
				}
				continue
			}
			checked[word] |= bit
		}
		rankedChildren[write] = rankedChildren[read]
		write++
	}
	return rankedChildren[:write]
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
			if provider := r.executionCapability(i).cached; provider != nil {
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
	plan := r.localPlan(pool)
	if plan == nil || !plan.valid {
		return false, false
	}
	if len(r.planningProviders) == 0 && r.planningPrepared {
		for rank, childIdx := range plan.order {
			ranked := rankedBitmap{card: ^uint64(0), childIdx: childIdx}
			if provider := r.executionCapability(childIdx).cached; provider != nil {
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

func validLocalPlanOrder(order []int, children int) bool {
	if len(order) != children {
		return false
	}
	// The allocation-free common path has at most eight children. Larger
	// groups are already on the pooled ranked-buffer path, so this quadratic
	// validation remains bounded by the compiled schema and needs no scratch.
	for i, child := range order {
		if child < 0 || child >= children {
			return false
		}
		for previous := 0; previous < i; previous++ {
			if order[previous] == child {
				return false
			}
		}
	}
	return true
}

func (r *allRule[T]) localPlan(pool *bitmapPool) *localAllPlan {
	return pool.allPlans[r]
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
	provider := r.executionCapability(childIdx).cached
	if provider == nil {
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
	plan := pool.allPlans[r]
	if plan == nil {
		if pool.allPlans == nil {
			pool.allPlans = make(map[any]*localAllPlan)
		}
		plan = &localAllPlan{order: make([]int, len(rankedChildren))}
		pool.allPlans[r] = plan
	}
	for i, ranked := range rankedChildren {
		plan.order[i] = ranked.childIdx
	}
	// The plan is written only here. Validate the permutation once when it
	// changes instead of repeating the quadratic proof on every warm search.
	plan.valid = validLocalPlanOrder(plan.order, len(r.children))
	plan.firstCard = rankedChildren[0].card
}

func (r *allRule[T]) loadLocalResult(
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) *localAllResult {
	if pool.local == nil || pool.observeRuntime {
		return nil
	}
	plan := pool.allPlans[r]
	if plan == nil {
		return nil
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
			return result
		}
	}
	return nil
}

//nolint:gocognit,nestif // Query-key validation keeps the cache-hit path allocation-free.
func (r *allRule[T]) loadLocalQueryResult(pool *bitmapPool, value T) *localAllResult {
	if pool.local == nil || pool.observeRuntime {
		return nil
	}
	plan := pool.allPlans[r]
	if plan == nil {
		return nil
	}
	for i := range plan.results {
		result := &plan.results[i]
		if result.bits == nil || result.epoch != pool.cacheEpoch {
			continue
		}
		match := true
		if r.queryKeyProviders != nil {
			if len(result.keys) != len(r.queryKeyProviders) {
				continue
			}
			for keyIndex, provider := range r.queryKeyProviders {
				if provider == nil || !provider.localQueryKeyMatches(value, result.keys[keyIndex]) {
					match = false
					break
				}
			}
		} else if len(result.keys) == len(r.children) {
			for child := range r.children {
				provider := r.executionCapability(child).queryKey
				if provider == nil || !provider.localQueryKeyMatches(value, result.keys[child]) {
					match = false
					break
				}
			}
		} else {
			match = false
		}
		if match {
			return result
		}
	}
	return nil
}

func (r *allRule[T]) captureLocalQueryKeys(value T) ([]any, uint64, bool) {
	if r.queryKeyProviders == nil {
		keys := make([]any, len(r.children))
		var bytes uint64
		for child := range r.children {
			provider := r.executionCapability(child).queryKey
			if provider == nil {
				return nil, 0, false
			}
			key, retained := provider.localQueryKey(value)
			keys[child] = key
			bytes = saturatingAdd(bytes, retained)
		}
		return keys, bytes, true
	}
	keys := make([]any, len(r.queryKeyProviders))
	var bytes uint64
	for keyIndex, provider := range r.queryKeyProviders {
		if provider == nil {
			return nil, 0, false
		}
		key, retained := provider.localQueryKey(value)
		keys[keyIndex] = key
		bytes = saturatingAdd(bytes, retained)
	}
	return keys, bytes, true
}

func (r *allRule[T]) storeLocalResult(
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	bits *roaring.Bitmap,
	value T,
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
	keys, _, keysSet := r.captureLocalQueryKeys(value)
	cardinality := bits.GetCardinality()
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
	if keysSet {
		entry.keys = keys
	} else {
		entry.keys = nil
	}
	if uint64(cap(entry.ids)) < cardinality {
		entry.ids = make([]uint32, 0, cardinality)
	} else {
		entry.ids = entry.ids[:0]
	}
	bits.Iterate(func(id uint32) bool {
		entry.ids = append(entry.ids, id)
		return true
	})
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
