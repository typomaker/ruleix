package ruleix

// orderedBoundaryQuantizer marks the comparator-backed quantization mode. Its
// boundaries are the keys of the common orderedIndex itself, so it neither
// duplicates values nor introduces separately accounted search metadata.
type orderedBoundaryQuantizer[V any] struct{}

func roundedOrderedBoundary[V any](index *orderedIndex[V], value V, upward bool) V {
	if len(index.blocks) == 0 {
		return value
	}
	blockIndex := index.blockFor(value)
	block := &index.blocks[blockIndex]
	position := index.searchBlock(block, value)
	if position < len(block.items) && index.compare(block.items[position].value, value) == 0 {
		return block.items[position].value
	}
	if upward {
		if position < len(block.items) {
			return block.items[position].value
		}
		if blockIndex+1 < len(index.blocks) {
			return index.blocks[blockIndex+1].items[0].value
		}
		return value
	}
	if position > 0 {
		return block.items[position-1].value
	}
	if blockIndex > 0 {
		previous := index.blocks[blockIndex-1].items
		return previous[len(previous)-1].value
	}
	return value
}

func rebuildOrderedBoundaries[V any](index *orderedIndex[V], dir direction) orderedIndex[V] {
	items := make([]*orderedItem[V], 0, index.buildStatistics().uniqueValues)
	for _, block := range index.blocks {
		items = append(items, block.items...)
	}
	next := newOrderedIndex(index.compare)
	for first := 0; first < len(items); first += 2 {
		last := min(first+2, len(items))
		boundary := items[first].value
		if dir == lessThan {
			boundary = items[last-1].value
		}
		bits := items[first].bits.Clone()
		for position := first + 1; position < last; position++ {
			bits.Or(items[position].bits)
		}
		next.insertPosting(boundary, bits)
	}
	return next
}
