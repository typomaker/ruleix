package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Search appends the unique IDs of every stored rule matching value to dst,
// reports whether this call found any matches, and updates the slice through
// its pointer if append allocates a larger backing array. Existing elements in
// dst do not affect the reported result. Results preserve first-insertion
// order. Search panics when dst is nil.
func (ix *Index[C, ID]) Search(value C, dst *[]ID) bool {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	return ix.search(value, dst, ix.pool)
}

// Local returns a search context that initially caches up to two recently
// repeated intermediate bitmap results per filter node and can adapt to four
// for a repeatedly reused working set. A value is admitted after its second
// recent use, so one-off queries do not retain their result bitmaps. It
// can reduce repeated work when adjacent searches share constraint values, at
// the cost of retaining admitted bitmaps for the lifetime of the Local.
//
// A Local is not safe for concurrent use. Create one per goroutine:
//
//	local := index.Local()
//	var matches []ID
//	for value := range values {
//		local.Search(value, &matches)
//	}
//
// Call Close when the context is no longer needed so its internal resources
// can be reused. The Index remains immutable and may be shared by all of those
// goroutines.
func (ix *Index[C, ID]) Local() *Local[C, ID] {
	localOrdinal := ix.localTelemetry.Add(1)
	sampled := localOrdinal%64 == 0
	observed := (ix.rootMetrics != nil || ix.localInspectors != nil) && sampled
	pools := &ix.locals
	if observed {
		pools = &ix.observedLocals
	}
	pool, _ := pools.Get().(*bitmapPool)
	if pool == nil {
		if observed {
			pool = newLocalBitmapPool(ix.nodes, ix.localInspectors)
		} else {
			pool = newLocalBitmapPool(ix.nodes)
		}
	}
	if observed {
		pool.bindRootInspector(ix.rootMetrics)
	}
	return &Local[C, ID]{index: ix, pool: pool, observed: observed}
}

// Search appends matching IDs to dst while reusing this Local's cached state
// and reports whether this call found any matches. Existing elements in dst do
// not affect the reported result. Search panics when dst is nil.
func (local *Local[C, ID]) Search(value C, dst *[]ID) bool {
	local.requireOpen()
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	return local.index.search(value, dst, local.pool)
}

// Close releases cached search results and returns the internal context to the
// originating Index for reuse. Empty per-node cache structures and learned All
// child orders remain with that recyclable context, while all admission and
// replacement state is reset so its next Local lifetime starts cold. A closed
// Local must not be used again. Repeated calls to Close are safe.
func (local *Local[C, ID]) Close() {
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

// Visit calls yield for matching IDs while reusing this Local's cached state.
// A nil yield function is a no-op.
func (local *Local[C, ID]) Visit(value C, yield func(ID) bool) {
	local.requireOpen()
	if yield == nil {
		return
	}
	root, exclusions := local.index.root, local.index.exclusions
	if local.observed {
		root, exclusions = local.index.observedRoot, local.index.observedExclusions
	}
	visitMatches(root, local.index.values, local.index.idChunkShift, local.pool, exclusions, value, yield)
}

func (local *Local[C, ID]) requireOpen() {
	if local == nil || local.index == nil || local.closed {
		panic("ruleix: closed Local")
	}
}

// Visit calls yield once for each unique matching ID in first-match order.
// Iteration stops immediately when yield returns false. A nil yield function is
// a no-op.
func (ix *Index[C, ID]) Visit(value C, yield func(ID) bool) {
	if yield == nil {
		return
	}
	visitMatches(ix.root, ix.values, ix.idChunkShift, ix.pool, ix.exclusions, value, yield)
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
			searchAllMatches(root, ix.values, ix.idChunkShift, pool, exclusions, value, dst, ix.rootMetrics)
			metrics.observeCardinality(uint64(len(*dst) - before))
			return len(*dst) != before
		}
	}
	if all, ok := root.(*allRule[C]); ok {
		searchAllMatches(all, ix.values, ix.idChunkShift, pool, exclusions, value, dst, nil)
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
	*dst = appendChunkedBitmapValues(bits, ix.values, ix.idChunkShift, *dst)
	if pool.observeRuntime && ix.rootMetrics != nil {
		pool.rootInspectorObserver(ix.rootMetrics).observeCardinality(uint64(len(*dst) - before))
	}
	return len(*dst) != before
}

//nolint:gocognit // The specialized execution branches avoid allocations in hot paths.
func searchAllMatches[C any, ID comparable](
	root *allRule[C],
	values []ID,
	idChunkShift uint8,
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
				result = appendChunkValues(result, values, id, idChunkShift)
			}
			*dst = result
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
	if !root.rankChildren(value, pool, rankedChildren) || len(rankedChildren) == 0 {
		if buffer != nil {
			pool.putRanked(buffer)
		}
		*dst = result
		return
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
			if cachedResult != nil && cachedResult.idsSet && excluded == nil {
				for _, id := range cachedResult.ids {
					result = appendChunkValues(result, values, id, idChunkShift)
				}
			} else {
				if cachedResult != nil {
					candidates = pool.get()
					candidates.Or(cachedResult.bits)
				}
				if excluded != nil {
					candidates.AndNot(excluded)
				}
				result = appendChunkedBitmapValues(candidates, values, idChunkShift, result)
			}
			if candidates != nil {
				pool.put(candidates)
			}
		} else {
			result = appendBitmapAllMatches(rankedChildren, excluded, values, idChunkShift, pool, result)
		}
	} else {
		result = appendScannedAllMatches(
			root, rankedChildren, exclusions, excluded, value, values, idChunkShift, pool, result,
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
