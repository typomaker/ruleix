package ruleix

func (*betweenRule[T, V]) streamingLossyAccumulator() {}

func (r *betweenRule[T, V]) quantizedStreamingDetails(details inspectionDetails) inspectionDetails {
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
	usage, apply, ok := clone.prepareStreamingNext()
	if !ok {
		return 0, nil, false
	}
	apply()
	return usage, clone, true
}

func cloneBetweenRule[T any, V any](r *betweenRule[T, V]) *betweenRule[T, V] {
	clone := *r
	from, until := *r.from, *r.until
	from.index, until.index = r.from.index.cloneBuild(), r.until.index.cloneBuild()
	clone.from, clone.until = &from, &until
	return &clone
}

func (r *betweenRule[T, V]) selectStreamingSide() *orderedRule[T, V] {
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
	if side.index.buildStatistics().uniqueValues <= 1 {
		return false
	}
	return side.level == 0 || side.transform.kind != orderedFixedTransformer ||
		side.level < side.transform.fixed.terminalLevel()
}

func (r *betweenRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		if _, apply, ok := r.prepareStreamingNext(); ok {
			apply()
		} else {
			return
		}
	}
}
func (r *betweenRule[T, V]) fitStreamingNext() {
	if _, apply, ok := r.prepareStreamingNext(); ok {
		apply()
	}
}
func (r *betweenRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}
func (r *betweenRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	selected := r.selectStreamingSide()
	if selected == nil {
		return 0, nil, false
	}
	var usage uint64
	var replacement *orderedRule[T, V]
	if selected.level == 0 {
		nextUsage, next, ok := selected.prepareStreamingFirstGeneration()
		if !ok {
			return 0, nil, false
		}
		replacement = next.(*orderedRule[T, V])
		usage = nextUsage
	} else {
		clone := *selected
		clone.index = selected.index.cloneBuild()
		_, apply, ok := clone.prepareStreamingNext()
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
