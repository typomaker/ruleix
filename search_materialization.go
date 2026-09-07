package ruleix

import "github.com/RoaringBitmap/roaring/v2"

func prepareRankedAllCandidates[C any](
	root *allRule[C],
	value C,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	candidates *roaring.Bitmap,
	metrics *inspectorRuntime,
) bool {
	if rankedChildren[0].card > allCandidateScanLimit {
		return root.intersectRankedInOrderObserved(value, candidates, pool, rankedChildren, metrics, nil)
	}
	if rankedChildren[0].bits != nil {
		if root.directIDComplete {
			return true
		}
		return root.materializeUnsupportedRemaining(value, pool, rankedChildren[1:])
	}
	bits := pool.get()
	root.children[rankedChildren[0].childIdx].search(value, bits, pool)
	rankedChildren[0].bits = bits
	rankedChildren[0].card = bits.GetCardinality()
	rankedChildren[0].owned = true
	if rankedChildren[0].card <= allCandidateScanLimit {
		if root.directIDComplete {
			return true
		}
		return root.materializeUnsupportedRemaining(value, pool, rankedChildren[1:])
	}
	return materializeRankedAfterFirst(root, value, pool, rankedChildren, metrics)
}

func materializeRankedAfterFirst[C any](
	root *allRule[C],
	value C,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	metrics *inspectorRuntime,
) bool {
	for i := 1; i < len(rankedChildren); i++ {
		bits := pool.get()
		root.children[rankedChildren[i].childIdx].search(value, bits, pool)
		rankedChildren[i].bits = bits
		rankedChildren[i].card = bits.GetCardinality()
		rankedChildren[i].owned = true
		if bits.IsEmpty() {
			return false
		}
		if i == 1 && shouldPruneBitmapRanges(pool) && bitmapRangesDisjoint(rankedChildren[0].bits, bits) {
			observeRangePruning(metrics, pool)
			return false
		}
	}
	return true
}

func buildAllExclusions[C any](
	rules []exclusionRule[C],
	value C,
	candidates uint64,
	pool *bitmapPool,
) *roaring.Bitmap {
	// Direct exclusion checks only run in appendScannedAllMatches. Bitmap
	// execution always needs an exclusion bitmap, even when the candidate set
	// is below the otherwise profitable direct-lookup limit.
	direct := candidates <= allDirectExclusionScanLimit && candidates <= allCandidateScanLimit
	if len(rules) == 0 || direct {
		return nil
	}
	excluded := pool.get()
	addExclusions(rules, value, excluded, pool)
	return excluded
}

func appendBitmapAllMatches[ID comparable](
	rankedChildren []rankedBitmap,
	excluded *roaring.Bitmap,
	values []ID,
	pool *bitmapPool,
	result []ID,
) []ID {
	// FastAnd can return the final result directly here. The generic All search
	// cannot use it efficiently because it must copy that result into dst.
	var inline [8]*roaring.Bitmap
	if len(rankedChildren) > len(inline) {
		bits := pool.get()
		bits.Or(rankedChildren[0].bits)
		for _, child := range rankedChildren[1:] {
			if bits.IsEmpty() {
				break
			}
			bits.And(child.bits)
		}
		if excluded != nil {
			bits.AndNot(excluded)
		}
		result = appendBitmapValues(bits, values, result)
		pool.put(bits)
		return result
	}
	postings := inline[:len(rankedChildren)]
	for i := range rankedChildren {
		postings[i] = rankedChildren[i].bits
	}
	bits := roaring.FastAnd(postings...)
	if excluded != nil {
		bits.AndNot(excluded)
	}
	result = appendBitmapValues(bits, values, result)
	return result
}

// Below this size Iterate avoids an iterator allocation and its callback cost
// is lower than the batch setup. Wide results benefit substantially from
// decoding IDs in batches.
const manyIteratorCardinalityThreshold = 4 << 10

func appendBitmapValues[ID comparable](bits *roaring.Bitmap, values []ID, result []ID) []ID {
	if bits.GetCardinality() < manyIteratorCardinalityThreshold {
		bits.Iterate(func(id uint32) bool {
			result = append(result, values[id])
			return true
		})
		return result
	}

	iterator := bits.ManyIterator()
	var ids [256]uint32
	for count := iterator.NextMany(ids[:]); count != 0; count = iterator.NextMany(ids[:]) {
		for _, id := range ids[:count] {
			result = append(result, values[id])
		}
	}
	return result
}

func appendScannedAllMatches[C any, ID comparable](
	root *allRule[C],
	rankedChildren []rankedBitmap,
	exclusions []exclusionRule[C],
	excluded *roaring.Bitmap,
	value C,
	values []ID,
	pool *bitmapPool,
	result []ID,
) []ID {
	rankedChildren[0].bits.Iterate(func(id uint32) bool {
		if excluded != nil && excluded.Contains(id) || excluded == nil && isExcluded(exclusions, value, id, pool) {
			return true
		}
		matches := true
		for _, child := range rankedChildren[1:] {
			if child.bits != nil {
				if !child.bits.Contains(id) {
					matches = false
					break
				}
				continue
			}
			if !root.matchesChildID(child.childIdx, value, id, pool) {
				matches = false
				break
			}
		}
		if matches {
			result = append(result, values[id])
		}
		return true
	})
	return result
}

func visitMatches[C any, ID comparable](
	root Rule[C],
	values []ID,
	pool *bitmapPool,
	exclusions []exclusionRule[C],
	value C,
	yield func(ID) bool,
) {
	bits := pool.get()
	defer pool.put(bits)
	root.search(value, bits, pool)
	if len(exclusions) != 0 {
		excluded := pool.get()
		addExclusions(exclusions, value, excluded, pool)
		bits.AndNot(excluded)
		pool.put(excluded)
	}
	bits.Iterate(func(id uint32) bool { return yield(values[id]) })
}

func addExclusions[C any](rules []exclusionRule[C], value C, dst *roaring.Bitmap, pool *bitmapPool) {
	for _, rule := range rules {
		rule.exclude(value, dst, pool)
	}
}

func isExcluded[C any](rules []exclusionRule[C], value C, id uint32, pool *bitmapPool) bool {
	for _, rule := range rules {
		if observed, ok := rule.(*inspectedExclusionRule[C]); ok {
			pool.inspectorObserver(observed.metrics).candidateCheck()
			rule = observed.child
		}
		if rule.isExcluded(value, id) {
			return true
		}
	}
	return false
}
