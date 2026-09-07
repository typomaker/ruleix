package ruleix

import "github.com/RoaringBitmap/roaring/v2"

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
	metricAliases []*inspectorRuntime,
) bool {
	if pool.local == nil && r.sharedWildcardGroups != nil {
		rankedChildren = r.collectSharedWildcards(v, pool, rankedChildren)
	}
	for i := range rankedChildren {
		if i > 0 {
			next := i + selectNextBitmapOperation(rankedChildren[i:])
			rankedChildren[i], rankedChildren[next] = rankedChildren[next], rankedChildren[i]
		}
		bits := rankedChildren[i].bits
		//nolint:nestif // Operation selection keeps filtering and replanning adjacent.
		if i > 0 {
			if i == 1 {
				dst.Or(rankedChildren[0].bits)
			}
			if shouldFilterCandidates(dst.GetSerializedSizeInBytes(), rankedChildren[i]) &&
				r.filterCandidates(rankedChildren[i].childIdx, v, dst, pool) {
				if dst.IsEmpty() {
					return false
				}
				if r.shouldValidateRemaining(dst.GetCardinality(), rankedChildren[i+1:]) {
					return r.validateCandidateBitmap(v, dst, pool, rankedChildren[i+1:])
				}
				continue
			}
		}
		if bits == nil {
			bits = pool.get()
			if r.executionCounters != nil {
				r.executionCounters.materializations++
				r.executionCounters.physicalInspectorSearches++
			}
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
			dst.Or(rankedChildren[i].bits)
			rankedChildren[0], rankedChildren[i] = rankedChildren[i], rankedChildren[0]
			return r.validateCandidateBitmap(v, dst, pool, rankedChildren[1:])
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
			for _, alias := range metricAliases {
				observeRangePruning(alias, pool)
			}
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
		if r.executionCounters != nil {
			r.executionCounters.intersections++
		}
		if dst.IsEmpty() {
			for j := range rankedChildren {
				if rankedChildren[j].owned {
					pool.put(rankedChildren[j].bits)
					rankedChildren[j].owned = false
				}
			}
			return false
		}
		if r.shouldValidateRemaining(dst.GetCardinality(), rankedChildren[i+1:]) {
			return r.validateCandidateBitmap(v, dst, pool, rankedChildren[i+1:])
		}
	}
	if len(rankedChildren) == 1 {
		dst.Or(rankedChildren[0].bits)
	}
	return true
}

// validateCandidateBitmap preserves insertion order by iterating the narrowed
// candidate bitmap and appending accepted IDs to a fresh pooled bitmap. It is
// selected only when the measured cardinality makes direct checks cheaper than
// materializing the remaining complete results.
func (r *allRule[T]) validateCandidateBitmap(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	remaining []rankedBitmap,
) bool {
	if !r.directIDComplete {
		r.materializeUnsupportedRemaining(v, pool, remaining)
	}
	r.orderCandidateValidation(dst.GetCardinality(), remaining)
	accepted := pool.get()
	dst.Iterate(func(id uint32) bool {
		for _, ranked := range remaining {
			if ranked.bits != nil {
				if r.executionCounters != nil {
					r.executionCounters.containsChecks++
				}
				observeCandidateCheck(r.children[ranked.childIdx], pool)
				if !ranked.bits.Contains(id) {
					return true
				}
				continue
			}
			if !r.matchesChildID(ranked.childIdx, v, id, pool) {
				return true
			}
		}
		accepted.Add(id)
		return true
	})
	dst.Clear()
	dst.Or(accepted)
	pool.put(accepted)
	return !dst.IsEmpty()
}

// materializeUnsupportedRemaining is shared by bitmap-returning and direct
// result assembly. It guarantees that an unsupported child is acquired once
// for the whole candidate batch, never once per candidate ID.
func (r *allRule[T]) materializeUnsupportedRemaining(v T, pool *bitmapPool, remaining []rankedBitmap) bool {
	for i := range remaining {
		if remaining[i].bits != nil || r.supportsDirectIDMatch(remaining[i].childIdx) {
			continue
		}
		bits := pool.get()
		r.children[remaining[i].childIdx].search(v, bits, pool)
		remaining[i].bits = bits
		remaining[i].card = bits.GetCardinality()
		remaining[i].owned = true
		if bits.IsEmpty() {
			return false
		}
	}
	return true
}

// orderCandidateValidation uses a plan that is deliberately independent of
// bitmap execution order. A child with a smaller expected surviving fraction
// rejects more candidates; an immutable bitmap lookup is cheaper than invoking
// a representation matcher. Comparing the ratios by cross multiplication
// keeps planning allocation-free and avoids floating-point work on the hot
// path. Unknown estimates retain schema order behind costed checks.
func (r *allRule[T]) orderCandidateValidation(candidates uint64, remaining []rankedBitmap) {
	for i := 1; i < len(remaining); i++ {
		for j := i; j > 0 && r.candidateValidationBefore(candidates, remaining[j], remaining[j-1]); j-- {
			remaining[j], remaining[j-1] = remaining[j-1], remaining[j]
		}
	}
}

func (r *allRule[T]) candidateValidationBefore(candidates uint64, left, right rankedBitmap) bool {
	leftRejected, leftCost := r.candidateValidationScore(candidates, left)
	rightRejected, rightCost := r.candidateValidationScore(candidates, right)
	return saturatingMul(leftRejected, rightCost) > saturatingMul(rightRejected, leftCost)
}

func (r *allRule[T]) candidateValidationScore(candidates uint64, ranked rankedBitmap) (rejected, cost uint64) {
	if ranked.card != ^uint64(0) && ranked.card < candidates {
		rejected = candidates - ranked.card
	}
	if ranked.bits != nil {
		return rejected, 1
	}
	if r.supportsDirectIDMatch(ranked.childIdx) {
		return rejected, 2
	}
	return rejected, 4
}

func (r *allRule[T]) filterCandidates(index int, value T, dst *roaring.Bitmap, pool *bitmapPool) bool {
	if pool.local != nil {
		return false
	}
	filter := r.executionCapability(index).filter
	if filter == nil {
		return false
	}
	filter.filterCandidates(value, dst, pool)
	return true
}

func (r *allRule[T]) matchesChildID(index int, value T, id uint32, pool *bitmapPool) bool {
	matcher := r.executionCapability(index).directID
	if matcher == nil {
		return false
	}
	observeCandidateCheck(r.children[index], pool)
	return matcher.matchesID(value, id)
}

func observeCandidateCheck[T any](rule Rule[T], pool *bitmapPool) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		pool.inspectorObserver(observed.metrics).candidateCheck()
		for _, alias := range observed.aliases {
			pool.inspectorObserver(alias).candidateCheck()
		}
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
	// An earlier intersection may empty the retained destination before the
	// observed executor reaches its next range-pruning probe. Roaring's extrema
	// methods panic for an empty bitmap; emptiness itself is already a complete
	// disjointness proof.
	if first.IsEmpty() || second.IsEmpty() {
		return true
	}
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
			other.intersectConcreteMatches(v, bits, pool)
			if bits.IsEmpty() {
				break
			}
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
		provider := r.duplicateEquality.providers[childIdx]
		// The nested inline/overflow split keeps the usual path allocation-free.
		//nolint:nestif
		if provider != nil {
			wildcard, posting, deduplicable := provider.lookupEqualityResultComponents(v)
			wildcardID := r.duplicateEquality.bitmapIDs[wildcard]
			postingID := r.duplicateEquality.bitmapIDs[posting]
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
						previousProvider := r.duplicateEquality.providers[rankedChildren[previous].childIdx]
						if previousProvider == nil {
							continue
						}
						previousWildcard, previousPosting, ok := previousProvider.lookupEqualityResultComponents(v)
						if ok && r.duplicateEquality.bitmapIDs[previousWildcard] == wildcardID &&
							r.duplicateEquality.bitmapIDs[previousPosting] == postingID {
							duplicate = true
							break
						}
					}
				}
			}
		}
		if duplicate {
			if r.executionCounters != nil {
				r.executionCounters.skippedOperands++
			}
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
