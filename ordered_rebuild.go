package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type orderedMergeCandidate struct {
	position         int
	cardinality      uint64
	postingFootprint uint64
}

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

func bestOrderedMerge[V any](index *orderedIndex[V], _ direction) (orderedMergeCandidate, bool) {
	// TODO: Compare build-only merge depth for new edge keys and a quality floor
	// that can make an otherwise impossible hard limit fail explicitly.
	items := make([]*orderedItem[V], 0, index.buildStatistics().uniqueValues)
	for _, block := range index.blocks {
		items = append(items, block.items...)
	}
	if len(items) < 2 {
		return orderedMergeCandidate{}, false
	}
	best := orderedMergeCandidate{position: -1}
	for position := 0; position+1 < len(items); position++ {
		left, right := items[position], items[position+1]
		cardinality := left.bits.GetCardinality() + right.bits.GetCardinality() -
			left.bits.AndCardinality(right.bits)
		footprint := bitmapBytes(left.bits) + bitmapBytes(right.bits)
		if best.position < 0 || cardinality < best.cardinality ||
			cardinality == best.cardinality && footprint > best.postingFootprint {
			best = orderedMergeCandidate{
				position: position, cardinality: cardinality, postingFootprint: footprint,
			}
		}
	}
	return best, true
}

func betterOrderedMerge(candidate orderedMergeCandidate, candidateOK bool, current orderedMergeCandidate, currentOK bool) bool {
	return candidateOK && (!currentOK || candidate.cardinality < current.cardinality ||
		candidate.cardinality == current.cardinality && candidate.postingFootprint > current.postingFootprint)
}

func rebuildOrderedBoundaries[V any](index *orderedIndex[V], dir direction) orderedIndex[V] {
	selected, ok := bestOrderedMerge(index, dir)
	if !ok {
		return index.cloneBuild()
	}
	currentAccounting := orderedQuantizedBuildAccounting(index)
	next := *index
	next.blocks = append([]orderedBlock[V](nil), index.blocks...)
	next.blockPrefix = nil
	next.rangeBlocks = nil
	next.routing = orderedRouting{}
	leftBlock, leftPosition := orderedItemPosition(index, selected.position)
	rightBlock, rightPosition := orderedItemPosition(index, selected.position+1)
	left := index.blocks[leftBlock].items[leftPosition]
	right := index.blocks[rightBlock].items[rightPosition]
	boundary := left.value
	if dir == lessThan {
		boundary = right.value
	}
	bits := left.bits.Clone()
	bits.Or(right.bits)
	merged := &orderedItem[V]{value: boundary, bits: bits}

	if leftBlock == rightBlock {
		oldAccounting := orderedBlockBuildAccounting(index.blocks[leftBlock])
		items := append([]*orderedItem[V](nil), index.blocks[leftBlock].items...)
		items[leftPosition] = merged
		items = append(items[:rightPosition], items[rightPosition+1:]...)
		next.blocks[leftBlock] = orderedBlock[V]{items: items, bits: aggregateOrderedItems(items)}
		next.buildAccounting = addOrderedBuildAccounting(
			subtractOrderedBuildAccounting(currentAccounting, oldAccounting),
			orderedBlockBuildAccounting(next.blocks[leftBlock]),
		)
		next.accountingValid = true
		return next
	}

	oldAccounting := addOrderedBuildAccounting(
		orderedBlockBuildAccounting(index.blocks[leftBlock]),
		orderedBlockBuildAccounting(index.blocks[rightBlock]),
	)
	leftItems := append([]*orderedItem[V](nil), index.blocks[leftBlock].items...)
	rightItems := append([]*orderedItem[V](nil), index.blocks[rightBlock].items...)
	if dir == greaterThan {
		leftItems[leftPosition] = merged
		rightItems = append(rightItems[:rightPosition], rightItems[rightPosition+1:]...)
	} else {
		leftItems = append(leftItems[:leftPosition], leftItems[leftPosition+1:]...)
		rightItems[rightPosition] = merged
	}
	next.blocks[leftBlock] = orderedBlock[V]{items: leftItems, bits: aggregateOrderedItems(leftItems)}
	next.blocks[rightBlock] = orderedBlock[V]{items: rightItems, bits: aggregateOrderedItems(rightItems)}
	if len(leftItems) == 0 {
		next.blocks = append(next.blocks[:leftBlock], next.blocks[leftBlock+1:]...)
	} else if len(rightItems) == 0 {
		next.blocks = append(next.blocks[:rightBlock], next.blocks[rightBlock+1:]...)
	}
	newAccounting := orderedBuildAccounting{}
	if len(leftItems) != 0 {
		newAccounting = addOrderedBuildAccounting(newAccounting, orderedBlockBuildAccounting(next.blocks[leftBlock]))
	}
	if len(rightItems) != 0 {
		rightIndex := rightBlock
		if len(leftItems) == 0 {
			rightIndex--
		}
		newAccounting = addOrderedBuildAccounting(newAccounting, orderedBlockBuildAccounting(next.blocks[rightIndex]))
	}
	next.buildAccounting = addOrderedBuildAccounting(
		subtractOrderedBuildAccounting(currentAccounting, oldAccounting), newAccounting,
	)
	next.accountingValid = true
	return next
}

func orderedItemPosition[V any](index *orderedIndex[V], position int) (int, int) {
	for blockIndex, block := range index.blocks {
		if position < len(block.items) {
			return blockIndex, position
		}
		position -= len(block.items)
	}
	return -1, -1
}

func aggregateOrderedItems[V any](items []*orderedItem[V]) *roaring.Bitmap {
	if len(items) == 1 {
		return items[0].bits
	}
	bits := roaring.New()
	for _, item := range items {
		bits.Or(item.bits)
	}
	return bits
}
