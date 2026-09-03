package ruleix

func (r *orderedRule[T, V]) streamingLossyAccumulator() {}

func (r *orderedRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		_, apply, ok := r.prepareStreamingNext()
		if !ok {
			break
		}
		apply()
	}
}

func (r *orderedRule[T, V]) fitStreamingNext() {
	if _, apply, ok := r.prepareStreamingNext(); ok {
		apply()
	}
}

func (r *orderedRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *orderedRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.index.buildStatistics().uniqueValues <= 1 {
		return 0, nil, false
	}
	next := rebuildOrderedBoundaries(&r.index, r.dir)
	details := (&orderedRule[T, V]{index: next, wildcard: r.wildcard}).
		refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, func() {
		r.index = next
		r.markBuildQuantized()
	}, true
}

func (r *orderedRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	if r.build != nil && r.build.quantized || r.index.buildStatistics().uniqueValues <= 1 {
		return 0, nil, false
	}
	candidate := &orderedRule[T, V]{
		nodeID: r.nodeID, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: r.wildcard, index: newOrderedIndex(r.compare),
		build: &orderedRuleBuildState{quantized: true},
	}
	candidate.index = rebuildOrderedBoundaries(&r.index, r.dir)
	details := candidate.refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, candidate, true
}
