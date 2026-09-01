package ruleix

func (r *quantizedOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (r *quantizedOrderedRule[T, V]) streamingLossyAccumulator()                          {}

func (r *quantizedOrderedRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && r.index.coarsenOne(r.dir) {
		r.lossyCapacity = r.index.buildStatistics().uniqueValues
	}
}

func (r *quantizedOrderedRule[T, V]) fitStreamingNext() {
	if r.lossyCapacity > 0 && r.index.coarsenOne(r.dir) {
		r.lossyCapacity = r.index.buildStatistics().uniqueValues
	}
}

func (r *quantizedOrderedRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *quantizedOrderedRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.lossyCapacity <= 0 {
		return 0, nil, false
	}
	clone := r.index.cloneBuild()
	if !clone.coarsenOne(r.dir) {
		return 0, nil, false
	}
	details := (&orderedRule[T, V]{index: clone, wildcard: r.wildcard, lossyCapacity: 1}).
		refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, func() {
		r.index = clone
		r.lossyCapacity = r.index.buildStatistics().uniqueValues
	}, true
}
