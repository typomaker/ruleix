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
	details.GranularityValue, details.GranularityAvailable = granularity, true
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
	for operator, index := range r.indexes {
		if index != nil {
			copy := index.cloneBuild()
			clone.indexes[operator] = &copy
		}
	}
	return &clone
}

func (r *compareByRule[T, V]) selectedStreamingIndex() int {
	selected := -1
	for operator, index := range r.indexes {
		if index == nil || index.buildStatistics().uniqueValues <= 1 ||
			r.quantizers[operator] != nil && r.levels[operator] >= r.quantizers[operator].terminalLevel() {
			continue
		}
		if selected < 0 || index.buildStatistics().uniqueValues > r.indexes[selected].buildStatistics().uniqueValues {
			selected = operator
		}
	}
	return selected
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
	selected := r.selectedStreamingIndex()
	if selected < 0 {
		return 0, nil, false
	}
	operator := Operator(selected)
	current := r.indexes[selected]
	next := newOrderedIndex(r.compare)
	nextLevel := r.levels[selected] + 1
	quantizer, boundaries := r.quantizers[selected], r.boundaries[selected]
	if quantizer == nil && boundaries == nil {
		compiled, ok := compileOrderedQuantizer[V]()
		if ok && orderedIndexAgreesWithQuantizer(current, compiled) {
			quantizer = &compiled
		} else {
			boundaries = &orderedBoundaryQuantizer[V]{}
		}
	}
	if boundaries != nil {
		next = rebuildOrderedBoundaries(current, compareByDirection(operator))
	} else {
		for _, block := range current.blocks {
			for _, item := range block.items {
				key := quantizer.rounded(item.value, nextLevel, compareByStorageUpward(operator))
				next.insertPosting(key, item.bits)
			}
		}
	}
	clone := cloneCompareByRule(r)
	clone.indexes[selected] = &next
	clone.quantizers[selected], clone.boundaries[selected], clone.levels[selected] = quantizer, boundaries, nextLevel
	usage := clone.quantizedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() {
		r.indexes[selected] = &next
		r.quantizers[selected], r.boundaries[selected], r.levels[selected] = quantizer, boundaries, nextLevel
	}, true
}

func orderedIndexAgreesWithQuantizer[V any](index *orderedIndex[V], quantizer orderedQuantizer[V]) bool {
	var previous V
	havePrevious := false
	for _, block := range index.blocks {
		for _, item := range block.items {
			if havePrevious && (index.compare(previous, item.value) >= 0 || quantizer.encode(previous) > quantizer.encode(item.value)) {
				return false
			}
			previous, havePrevious = item.value, true
		}
	}
	return true
}
