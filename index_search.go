package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// close releases cached search results and returns the internal context to the
// originating Index for reuse. Empty per-node cache structures and learned All
// child orders remain with that recyclable context, while all query and result
// state is reset so its next context lifetime starts cold. A closed
// context must not be used again. Repeated calls to close are safe.
func (local *localContext[C, ID]) close() {
	if local == nil || local.closed || local.index == nil {
		return
	}
	local.requireOpen()
	index := local.index
	local.pool.resetLocal()
	if local.observed {
		index.observedLocals.Put(local.pool)
	} else {
		index.locals.Put(local.pool)
	}
	local.index = nil
	local.pool = nil
	local.closed = true
}

func (local *localContext[C, ID]) requireOpen() {
	if local == nil || local.index == nil || local.closed {
		panic("ruleix: closed context")
	}
}

func (ix *Index[C, ID]) search(value C, dst *[]ID, pool *bitmapPool) bool {
	before := len(*dst)
	root, exclusions := ix.root, ix.exclusions
	if pool.observeRuntime {
		root, exclusions = ix.observedRoot, ix.observedExclusions
	}
	if pool.observeRuntime && ix.rootMetrics != nil {
		if root, ok := root.(*allRule[C]); ok {
			metrics := pool.rootInspectorObserver(ix.rootMetrics)
			searchAllMatches(root, ix.values, pool, exclusions, value, dst, ix.rootMetrics)
			metrics.observeCardinality(uint64(len(*dst) - before))
			return len(*dst) != before
		}
	}
	if all, ok := root.(*allRule[C]); ok {
		searchAllMatches(all, ix.values, pool, exclusions, value, dst, nil)
		return len(*dst) != before
	}
	bits := pool.get()
	defer pool.put(bits)
	root.search(value, bits, pool)
	if len(exclusions) != 0 {
		excluded := pool.get()
		addExclusions(exclusions, value, excluded, pool)
		bits.AndNot(excluded)
		pool.put(excluded)
	}
	*dst = appendBitmapValues(bits, ix.values, *dst)
	if pool.observeRuntime && ix.rootMetrics != nil {
		pool.rootInspectorObserver(ix.rootMetrics).observeCardinality(uint64(len(*dst) - before))
	}
	return len(*dst) != before
}

//nolint:gocognit // The specialized execution branches avoid allocations in hot paths.
func searchAllMatches[C any, ID comparable](
	root *allRule[C],
	values []ID,
	pool *bitmapPool,
	exclusions []exclusionRule[C],
	value C,
	dst *[]ID,
	metrics *inspectorRuntime,
) {
	result := *dst
	if len(exclusions) == 0 {
		if cached := root.loadLocalQueryResult(pool, value); cached != nil {
			for _, id := range cached.ids {
				result = append(result, values[id])
			}
			*dst = result
			return
		}
	}
	if mask := pool.candidateMask; len(exclusions) == 0 && mask != nil && mask.source != nil && !pool.observeRuntime {
		narrow := mask.count <= allCandidateScanLimit && root.narrowMaskPrepared
		if narrow && appendNarrowCandidateMaskMatches(root, value, mask, &pool.partial, values, dst) {
			return
		}
		if !narrow && appendCandidateMaskMatches(root, value, pool, values, dst) {
			return
		}
	}
	var inline [8]rankedBitmap
	var inlineChecked [1]uint64
	var rankedChildren []rankedBitmap
	var buffer *rankedBitmapBuffer
	if len(root.children) > len(inline) || root.equalityClassCount > 64 {
		buffer = pool.getRanked(len(root.children))
		rankedChildren = buffer.items
	} else {
		rankedChildren = inline[:len(root.children)]
	}
	if mask := pool.candidateMask; mask != nil && pool.allPlans == nil &&
		len(exclusions) == 0 && !pool.observeRuntime && root.executionCounters == nil {
		mask.first = emptyRankInProgress
	}
	ranked := root.rankChildren(value, pool, rankedChildren)
	if m := pool.candidateMask; m != nil && m.first == emptyRankInProgress {
		m.first = 0
	}
	if !ranked || len(rankedChildren) == 0 {
		if buffer != nil {
			pool.putRanked(buffer)
		}
		*dst = result
		return
	}
	if len(exclusions) == 0 && pool.candidateMask != nil &&
		(rankedChildren[0].card > allCandidateScanLimit || root.narrowMaskPrepared) {
		root.rememberCandidateMask(pool, value, rankedChildren[0])
	}
	// Dense identity lookup pays for itself while operands are cold. Once the
	// first ranked Local operand is cached, let the ordinary child caches admit
	// the remaining logical operands; stable warm searches then avoid both the
	// class lookup and physical materialization work.
	useEqualityClasses := root.equalityClassCount != 0 &&
		(pool.local == nil || rankedChildren[0].bits == nil)
	if useEqualityClasses {
		checked := inlineChecked[:]
		if buffer != nil {
			words := int((root.equalityClassCount + 63) / 64)
			if cap(buffer.mask) < words {
				buffer.mask = make([]uint64, words)
			} else {
				buffer.mask = buffer.mask[:words]
				clear(buffer.mask)
			}
			checked = buffer.mask
		}
		rankedChildren = root.deduplicateEqualityClasses(value, rankedChildren, checked)
	}
	initiallyBroad := rankedChildren[0].card > allCandidateScanLimit
	var candidates *roaring.Bitmap
	var cachedResult *localAllResult
	if initiallyBroad {
		cachedResult = root.loadLocalResult(pool, rankedChildren)
		if cachedResult == nil {
			candidates = pool.get()
		}
	}
	if cachedResult == nil && !prepareRankedAllCandidates(root, value, pool, rankedChildren, candidates, metrics) {
		if candidates != nil {
			pool.put(candidates)
		}
		root.releaseRanked(pool, rankedChildren)
		if buffer != nil {
			pool.putRanked(buffer)
		}
		*dst = result
		return
	}
	if candidates != nil && cachedResult == nil {
		root.storeLocalResult(pool, rankedChildren, candidates, value)
	}

	excluded := buildAllExclusions(exclusions, value, rankedChildren[0].card, pool)
	broad := rankedChildren[0].card > allCandidateScanLimit
	//nolint:nestif // Broad result assembly keeps ownership and exclusion handling together.
	if broad {
		if candidates != nil || cachedResult != nil {
			if cachedResult != nil && excluded == nil {
				for _, id := range cachedResult.ids {
					result = append(result, values[id])
				}
			} else {
				if cachedResult != nil {
					candidates = pool.get()
					candidates.Or(cachedResult.bits)
				}
				if excluded != nil {
					candidates.AndNot(excluded)
				}
				result = appendBitmapValues(candidates, values, result)
			}
			if candidates != nil {
				pool.put(candidates)
			}
		} else {
			result = appendBitmapAllMatches(rankedChildren, excluded, values, pool, result)
		}
	} else {
		result = appendScannedAllMatches(
			root, rankedChildren, exclusions, excluded, value, values, pool, result,
		)
	}
	if excluded != nil {
		pool.put(excluded)
	}
	root.releaseRanked(pool, rankedChildren)
	if buffer != nil {
		pool.putRanked(buffer)
	}
	*dst = result
}
