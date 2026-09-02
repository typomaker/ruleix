package ruleix

func (*quantizedBetweenRule[T, V]) streamingLossyAccumulator()                            {}
func (r *quantizedBetweenRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*quantizedBetweenRule[T, V]) validate(T) error                                      { return nil }
func (r *quantizedBetweenRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	descriptor := r.betweenRule.canonicalDescriptor()
	descriptor.schema = r
	return descriptor
}

func (r *quantizedBetweenRule[T, V]) inspectionStrategy() string { return "lossy-between-ordered" }
func (*quantizedBetweenRule[T, V]) inspectionMode() RuleMode     { return RuleModeLossy }
func (r *quantizedBetweenRule[T, V]) optimize(total uint64) Rule[T] {
	if r.from.wildcard.GetCardinality() == total && r.until.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.from.wildcard)
	}
	return r
}

func (r *quantizedBetweenRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	fromMemory, fromItems, _ := quantizedOrderedAccounting(&r.from.index, r.from.wildcard)
	untilMemory, untilItems, _ := quantizedOrderedAccounting(&r.until.index, r.until.wildcard)
	details.MemoryUsageBytes, details.MemoryUsageAvailable = fromMemory+untilMemory, true
	details.Items, details.ItemsAvailable = fromItems+untilItems, true
	details.GranularityValue = uint64(r.from.index.buildStatistics().uniqueValues +
		r.until.index.buildStatistics().uniqueValues)
	details.GranularityAvailable = true
	return details
}

func (r *quantizedBetweenRule[T, V]) selectStreamingSide() *orderedRule[T, V] {
	from := r.from.index.buildStatistics().uniqueValues
	until := r.until.index.buildStatistics().uniqueValues
	if from >= until && from > 1 {
		return r.from
	}
	if until > 1 {
		return r.until
	}
	return nil
}

func (r *quantizedBetweenRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		if r.selectStreamingSide() == nil {
			return
		}
		r.fitStreamingNext()
	}
}

func (r *quantizedBetweenRule[T, V]) fitStreamingNext() {
	side := r.selectStreamingSide()
	if side == nil {
		return
	}
	side.index.coarsenOne(side.dir)
	side.lossyCapacity = side.index.buildStatistics().uniqueValues
}

func (r *quantizedBetweenRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *quantizedBetweenRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	selected := r.selectStreamingSide()
	if selected == nil {
		return 0, nil, false
	}
	clone := *r.betweenRule
	from, until := *r.from, *r.until
	from.index, until.index = r.from.index.cloneBuild(), r.until.index.cloneBuild()
	clone.from, clone.until = &from, &until
	wrapped := &quantizedBetweenRule[T, V]{&clone}
	wrapped.fitStreamingNext()
	usage := wrapped.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() {
		r.from.index, r.from.lossyCapacity = clone.from.index, clone.from.lossyCapacity
		r.until.index, r.until.lossyCapacity = clone.until.index, clone.until.lossyCapacity
	}, true
}
