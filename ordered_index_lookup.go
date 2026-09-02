package ruleix

import "github.com/RoaringBitmap/roaring/v2"

func (i *orderedIndex[V]) exact(value V) *roaring.Bitmap {
	if len(i.blocks) == 0 {
		return nil
	}
	block := &i.blocks[i.blockFor(value)]
	pos := i.searchBlock(block, value)
	if pos == len(block.items) || i.compare(block.items[pos].value, value) != 0 {
		return nil
	}
	return block.items[pos].bits
}

// ceiling returns the posting whose outward-rounded upper boundary is the
// first boundary not less than value. It is used by quantized EQ only.
func (i *orderedIndex[V]) ceiling(value V) *roaring.Bitmap {
	if len(i.blocks) == 0 {
		return nil
	}
	block := &i.blocks[i.blockFor(value)]
	pos := i.searchBlock(block, value)
	if pos == len(block.items) {
		return nil
	}
	return block.items[pos].bits
}

func (i *orderedIndex[V]) walk(value V, ascending, inclusive bool, visit func(*roaring.Bitmap)) {
	if len(i.blocks) == 0 {
		return
	}
	blockIndex := i.blockFor(value)
	block := &i.blocks[blockIndex]
	if ascending {
		lo, hi := 0, len(block.items)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			cmp := i.compare(block.items[mid].value, value)
			if cmp < 0 || !inclusive && cmp == 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo == 0 {
			visit(block.bits)
		} else {
			for pos := lo; pos < len(block.items); pos++ {
				visit(block.items[pos].bits)
			}
		}
		i.walkBlockRange(blockIndex+1, len(i.blocks), visit)
		return
	}
	lo, hi := 0, len(block.items)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		cmp := i.compare(block.items[mid].value, value)
		if cmp < 0 || inclusive && cmp == 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(block.items) {
		visit(block.bits)
	} else {
		for pos := lo - 1; pos >= 0; pos-- {
			visit(block.items[pos].bits)
		}
	}
	i.walkBlockRange(0, blockIndex, visit)
}

func (i *orderedIndex[V]) walkBlockRange(first, last int, visit func(*roaring.Bitmap)) {
	for first < last && first%orderedRangeBlockSize != 0 {
		visit(i.blocks[first].bits)
		first++
	}
	for first+orderedRangeBlockSize <= last {
		rangeIndex := first / orderedRangeBlockSize
		if rangeIndex >= len(i.rangeBlocks) || i.rangeBlocks[rangeIndex].first != first {
			break
		}
		visit(i.rangeBlocks[rangeIndex].bits)
		first += orderedRangeBlockSize
	}
	for first < last {
		visit(i.blocks[first].bits)
		first++
	}
}

func (i *orderedIndex[V]) matches(value V, ascending, inclusive bool, id uint32) bool {
	if len(i.blocks) == 0 {
		return false
	}
	blockIndex := i.blockFor(value)
	block := &i.blocks[blockIndex]
	if ascending {
		lo, hi := 0, len(block.items)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			cmp := i.compare(block.items[mid].value, value)
			if cmp < 0 || !inclusive && cmp == 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo == 0 {
			if block.bits.Contains(id) {
				return true
			}
		} else {
			for pos := lo; pos < len(block.items); pos++ {
				if block.items[pos].bits.Contains(id) {
					return true
				}
			}
		}
		return i.blockRangeContains(blockIndex+1, len(i.blocks), id)
	}

	lo, hi := 0, len(block.items)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		cmp := i.compare(block.items[mid].value, value)
		if cmp < 0 || inclusive && cmp == 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(block.items) {
		if block.bits.Contains(id) {
			return true
		}
	} else {
		for pos := lo - 1; pos >= 0; pos-- {
			if block.items[pos].bits.Contains(id) {
				return true
			}
		}
	}
	return i.blockRangeContains(0, blockIndex, id)
}

func (i *orderedIndex[V]) blockRangeContains(first, last int, id uint32) bool {
	for first < last && first%orderedRangeBlockSize != 0 {
		if i.blocks[first].bits.Contains(id) {
			return true
		}
		first++
	}
	for first+orderedRangeBlockSize <= last {
		rangeIndex := first / orderedRangeBlockSize
		if rangeIndex >= len(i.rangeBlocks) || i.rangeBlocks[rangeIndex].first != first {
			break
		}
		if i.rangeBlocks[rangeIndex].bits.Contains(id) {
			return true
		}
		first += orderedRangeBlockSize
	}
	for first < last {
		if i.blocks[first].bits.Contains(id) {
			return true
		}
		first++
	}
	return false
}
