package ruleix

func (r *quantizedOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (r *quantizedOrderedRule[T, V]) streamingLossyAccumulator()                          {}

func (r *orderedRule[T, V]) storageValue(value V) V {
	if r.quantizer != nil {
		return r.quantizer.rounded(value, r.level, r.dir == lessThan)
	}
	if r.boundaries != nil {
		return roundedOrderedBoundary(&r.index, value, r.dir == lessThan)
	}
	return value
}

func (r *orderedRule[T, V]) searchValue(value V) V {
	if r.quantizer != nil {
		return r.quantizer.rounded(value, r.level, r.dir == greaterThan)
	}
	if r.boundaries != nil {
		return roundedOrderedBoundary(&r.index, value, r.dir == greaterThan)
	}
	return value
}

func (r *quantizedOrderedRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		_, apply, ok := r.prepareStreamingNext()
		if !ok {
			break
		}
		apply()
	}
}

func (r *quantizedOrderedRule[T, V]) fitStreamingNext() {
	if _, apply, ok := r.prepareStreamingNext(); ok {
		apply()
	}
}

func (r *quantizedOrderedRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *quantizedOrderedRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.quantizer == nil && (r.boundaries == nil || r.index.buildStatistics().uniqueValues <= 1) ||
		r.quantizer != nil && r.level >= r.quantizer.terminalLevel() {
		return 0, nil, false
	}
	next := newOrderedIndex(r.compare)
	if r.boundaries != nil {
		next = rebuildOrderedBoundaries(&r.index, r.dir)
	} else {
		for _, block := range r.index.blocks {
			for _, item := range block.items {
				boundary := r.quantizer.rounded(item.value, r.level+1, r.dir == lessThan)
				next.insertPosting(boundary, item.bits)
			}
		}
	}
	details := (&orderedRule[T, V]{index: next, wildcard: r.wildcard, lossyCapacity: 1}).
		refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, func() {
		r.index = next
		r.level++
		r.lossyCapacity = r.index.buildStatistics().uniqueValues
	}, true
}

// prepareStreamingFirstGeneration validates that the user comparator agrees
// with the numeric/time total order before opting into its fixed quantizer.
func (r *orderedRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	quantizer, ok := compileOrderedQuantizer[V]()
	if !ok {
		return r.prepareBoundaryFirstGeneration()
	}
	var previous V
	havePrevious := false
	for _, block := range r.index.blocks {
		for _, item := range block.items {
			comparatorReversed := havePrevious && r.compare(previous, item.value) >= 0
			encodingReversed := havePrevious && quantizer.encode(previous) > quantizer.encode(item.value)
			if comparatorReversed || encodingReversed {
				return r.prepareBoundaryFirstGeneration()
			}
			previous, havePrevious = item.value, true
		}
	}
	candidate := &orderedRule[T, V]{
		nodeID: r.nodeID, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: r.wildcard, index: newOrderedIndex(r.compare), lossyCapacity: 1,
		quantizer: &quantizer, level: 1,
	}
	for _, block := range r.index.blocks {
		for _, item := range block.items {
			candidate.index.insertPosting(candidate.storageValue(item.value), item.bits)
		}
	}
	details := candidate.refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, &quantizedOrderedRule[T, V]{candidate}, true
}

func (r *orderedRule[T, V]) prepareBoundaryFirstGeneration() (uint64, Rule[T], bool) {
	if r.index.buildStatistics().uniqueValues <= 1 {
		return 0, nil, false
	}
	candidate := &orderedRule[T, V]{
		nodeID: r.nodeID, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: r.wildcard, index: newOrderedIndex(r.compare), lossyCapacity: 1,
		boundaries: &orderedBoundaryQuantizer[V]{}, level: 1,
	}
	candidate.index = rebuildOrderedBoundaries(&r.index, r.dir)
	details := candidate.refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, &quantizedOrderedRule[T, V]{candidate}, true
}
