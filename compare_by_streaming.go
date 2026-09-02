package ruleix

import "github.com/RoaringBitmap/roaring/v2"

func (*quantizedCompareByRule[T, V]) streamingLossyAccumulator()                            {}
func (r *quantizedCompareByRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*quantizedCompareByRule[T, V]) validate(T) error                                      { return nil }
func (r *quantizedCompareByRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	descriptor := r.compareByRule.canonicalDescriptor()
	descriptor.schema = r
	return descriptor
}

func (r *quantizedCompareByRule[T, V]) inspectionStrategy() string { return "lossy-compare-by-ordered" }
func (*quantizedCompareByRule[T, V]) inspectionMode() RuleMode     { return RuleModeLossy }
func (r *quantizedCompareByRule[T, V]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	return r
}

func (r *quantizedCompareByRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(24) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	var granularity uint64
	for _, index := range r.indexes {
		if index == nil {
			continue
		}
		memory, count, _ := quantizedOrderedAccounting(index, roaring.New())
		usage += memory
		items += count
		granularity += uint64(index.buildStatistics().uniqueValues)
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = granularity, true
	return details
}

func (r *quantizedCompareByRule[T, V]) selectedStreamingIndex() int {
	selected := -1
	for operator, index := range r.indexes {
		if index != nil && index.buildStatistics().uniqueValues > 1 &&
			(selected < 0 || index.buildStatistics().uniqueValues > r.indexes[selected].buildStatistics().uniqueValues) {
			selected = operator
		}
	}
	return selected
}

func (r *quantizedCompareByRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		if r.selectedStreamingIndex() < 0 {
			return
		}
		r.fitStreamingNext()
	}
}

func (r *quantizedCompareByRule[T, V]) fitStreamingNext() {
	selected := r.selectedStreamingIndex()
	if selected < 0 {
		return
	}
	r.indexes[selected].coarsenOne(compareByDirection(Operator(selected)))
	r.lossyCapacity[selected] = r.indexes[selected].buildStatistics().uniqueValues
}

func (r *quantizedCompareByRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *quantizedCompareByRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	selected := r.selectedStreamingIndex()
	if selected < 0 {
		return 0, nil, false
	}
	clone := *r.compareByRule
	for operator, index := range r.indexes {
		if index != nil {
			copy := index.cloneBuild()
			clone.indexes[operator] = &copy
		}
	}
	clone.indexes[selected].coarsenOne(compareByDirection(Operator(selected)))
	clone.lossyCapacity[selected] = clone.indexes[selected].buildStatistics().uniqueValues
	wrapped := &quantizedCompareByRule[T, V]{&clone}
	usage := wrapped.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() {
		r.indexes = clone.indexes
		r.lossyCapacity = clone.lossyCapacity
	}, true
}
