package ruleix

func (*quantizedBetweenRule[T, V]) streamingLossyAccumulator()                            {}
func (r *quantizedBetweenRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*quantizedBetweenRule[T, V]) validate(T) error                                      { return nil }
func (r *quantizedBetweenRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	descriptor := r.betweenRule.canonicalDescriptor()
	descriptor.schema = r
	return descriptor
}
func (*quantizedBetweenRule[T, V]) inspectionMode() RuleMode { return RuleModeLossy }
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
	details.GranularityValue = uint64(r.from.index.buildStatistics().uniqueValues + r.until.index.buildStatistics().uniqueValues)
	details.GranularityAvailable = true
	return details
}

func (r *betweenRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	clone := cloneBetweenRule(r)
	candidate := &quantizedBetweenRule[T, V]{clone}
	usage, apply, ok := candidate.prepareStreamingNext()
	if !ok {
		return 0, nil, false
	}
	apply()
	return usage, candidate, true
}

func cloneBetweenRule[T any, V any](r *betweenRule[T, V]) *betweenRule[T, V] {
	clone := *r
	from, until := *r.from, *r.until
	from.index, until.index = r.from.index.cloneBuild(), r.until.index.cloneBuild()
	clone.from, clone.until = &from, &until
	return &clone
}

func (r *quantizedBetweenRule[T, V]) selectStreamingSide() *orderedRule[T, V] {
	fromAvailable := orderedStreamingAvailable(r.from)
	untilAvailable := orderedStreamingAvailable(r.until)
	if fromAvailable && (!untilAvailable || r.from.index.buildStatistics().uniqueValues >= r.until.index.buildStatistics().uniqueValues) {
		return r.from
	}
	if untilAvailable {
		return r.until
	}
	return nil
}

func orderedStreamingAvailable[T any, V any](side *orderedRule[T, V]) bool {
	return side.index.buildStatistics().uniqueValues > 1 &&
		(side.quantizer == nil || side.level < side.quantizer.terminalLevel())
}

func (r *quantizedBetweenRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		if _, apply, ok := r.prepareStreamingNext(); ok {
			apply()
		} else {
			return
		}
	}
}
func (r *quantizedBetweenRule[T, V]) fitStreamingNext() {
	if _, apply, ok := r.prepareStreamingNext(); ok {
		apply()
	}
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
	var usage uint64
	var replacement *orderedRule[T, V]
	if selected.quantizer == nil && selected.boundaries == nil {
		nextUsage, next, ok := selected.prepareStreamingFirstGeneration()
		if !ok {
			return 0, nil, false
		}
		replacement = next.(*quantizedOrderedRule[T, V]).orderedRule
		usage = nextUsage
	} else {
		clone := *selected
		clone.index = selected.index.cloneBuild()
		wrapped := &quantizedOrderedRule[T, V]{&clone}
		_, apply, ok := wrapped.prepareStreamingNext()
		if !ok {
			return 0, nil, false
		}
		apply()
		replacement = &clone
		usage = clone.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	}
	other := r.until
	if selected == r.until {
		other = r.from
	}
	otherUsage := other.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage + otherUsage, func() {
		if selected == r.from {
			r.from = replacement
		} else {
			r.until = replacement
		}
	}, true
}
