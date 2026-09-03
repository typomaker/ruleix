package ruleix

import "github.com/RoaringBitmap/roaring/v2"

func (*compareByRule[T, V]) streamingLossyAccumulator() {}

func (r *compareByRule[T, V]) quantizedStreamingDetails(details inspectionDetails) inspectionDetails {
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
	details.DistinctValues, details.DistinctValuesAvailable = granularity, true
	return details
}

func (r *compareByRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	clone := cloneCompareByRule(r)
	usage, apply, ok := clone.prepareStreamingNext()
	if !ok {
		return 0, nil, false
	}
	apply()
	return usage, clone, true
}

func cloneCompareByRule[T any, V any](r *compareByRule[T, V]) *compareByRule[T, V] {
	clone := *r
	if r.build != nil {
		state := *r.build
		clone.build = &state
	}
	for operator, index := range r.indexes {
		if index != nil {
			copy := index.cloneBuild()
			clone.indexes[operator] = &copy
		}
	}
	return &clone
}

func (r *compareByRule[T, V]) selectedStreamingIndex() (int, orderedMergeCandidate) {
	selected := -1
	var selectedCandidate orderedMergeCandidate
	for operator, index := range r.indexes {
		if index == nil {
			continue
		}
		candidate, available := bestOrderedMerge(index, compareByDirection(Operator(operator)))
		if betterOrderedMerge(candidate, available, selectedCandidate, selected >= 0) {
			selectedCandidate = candidate
			selected = operator
		}
	}
	return selected, selectedCandidate
}

func (r *compareByRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		if _, apply, ok := r.prepareStreamingNext(); ok {
			apply()
		} else {
			return
		}
	}
}
func (r *compareByRule[T, V]) fitStreamingNext() {
	if _, apply, ok := r.prepareStreamingNext(); ok {
		apply()
	}
}
func (r *compareByRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *compareByRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	selected, candidate := r.selectedStreamingIndex()
	if selected < 0 {
		return 0, nil, false
	}
	operator := Operator(selected)
	current := r.indexes[selected]
	next := rebuildSelectedOrderedBoundary(current, compareByDirection(operator), candidate)
	usage := uint64(24) + bitmapBytes(r.wildcard)
	for indexPosition, index := range r.indexes {
		if index == nil {
			continue
		}
		if indexPosition == selected {
			index = &next
		}
		memory, _, _ := quantizedOrderedAccounting(index, roaring.New())
		usage += memory
	}
	return usage, func() {
		r.indexes[selected] = &next
		r.markBuildQuantized(operator)
		if operator == OperatorEQ {
			r.equalityLookup = quantizedOrderedEquality[V]
		}
	}, true
}
