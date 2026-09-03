package ruleix

func (*betweenRule[T, V]) streamingLossyAccumulator() {}

func (r *betweenRule[T, V]) quantizedStreamingDetails(details inspectionDetails) inspectionDetails {
	fromMemory, fromItems, _ := quantizedOrderedAccounting(&r.from.index, r.from.wildcard)
	untilMemory, untilItems, _ := quantizedOrderedAccounting(&r.until.index, r.until.wildcard)
	details.MemoryUsageBytes, details.MemoryUsageAvailable = fromMemory+untilMemory, true
	details.Items, details.ItemsAvailable = fromItems+untilItems, true
	distinct := uint64(r.from.index.buildStatistics().uniqueValues + r.until.index.buildStatistics().uniqueValues)
	details.DistinctValues, details.DistinctValuesAvailable = distinct, true
	if r.inspectionMode() == RuleModeLossy {
		details.GranularityValue, details.GranularityAvailable = distinct, true
	}
	return details
}

func (r *betweenRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	if r.from.build != nil && r.from.build.quantized || r.until.build != nil && r.until.build.quantized {
		return 0, nil, false
	}
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
	from.build, until.build = cloneOrderedRuleBuildState(r.from.build), cloneOrderedRuleBuildState(r.until.build)
	clone.from, clone.until = &from, &until
	return &clone
}

func (r *betweenRule[T, V]) selectStreamingSide() (*orderedRule[T, V], orderedMergeCandidate) {
	fromCandidate, fromAvailable := bestOrderedMerge(&r.from.index, r.from.dir)
	untilCandidate, untilAvailable := bestOrderedMerge(&r.until.index, r.until.dir)
	if betterOrderedMerge(fromCandidate, fromAvailable, untilCandidate, untilAvailable) {
		return r.from, fromCandidate
	}
	if untilAvailable {
		return r.until, untilCandidate
	}
	return nil, orderedMergeCandidate{}
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
	selected, candidate := r.selectStreamingSide()
	if selected == nil {
		return 0, nil, false
	}
	var usage uint64
	var replacement *orderedRule[T, V]
	clone := *selected
	clone.index = rebuildSelectedOrderedBoundary(&selected.index, selected.dir, candidate)
	clone.build = &orderedRuleBuildState{quantized: true}
	replacement = &clone
	usage = clone.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
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
