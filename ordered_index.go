package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type orderedItem[V any] struct {
	value V
	bits  *roaring.Bitmap
}

type orderedIndex[V any] struct {
	compare            Compare[V]
	blocks             []orderedBlock[V]
	blockPrefix        []uint64
	firstBlockCapacity int
}

// orderedBlockSize is deliberately large enough to amortize wide scans while
// keeping the two unaggregated boundary fragments small.
const orderedBlockSize = 128

type orderedBlock[V any] struct {
	items []*orderedItem[V]
	bits  *roaring.Bitmap
}

func newOrderedIndex[V any](compare Compare[V]) orderedIndex[V] {
	return orderedIndex[V]{compare: compare}
}
func newOrderedIndexWithHint[V any](compare Compare[V], hint orderedBuildStatistics) orderedIndex[V] {
	itemCapacity := capacityHint(hint.uniqueValues)
	if itemCapacity > orderedBlockSize*2 {
		itemCapacity = orderedBlockSize * 2
	}
	return orderedIndex[V]{
		compare:            compare,
		blocks:             make([]orderedBlock[V], 0, capacityHint(hint.blocks)),
		firstBlockCapacity: itemCapacity,
	}
}
func (i *orderedIndex[V]) buildStatistics() orderedBuildStatistics {
	statistics := orderedBuildStatistics{blocks: len(i.blocks)}
	for block := range i.blocks {
		statistics.uniqueValues += len(i.blocks[block].items)
	}
	return statistics
}
func (i *orderedIndex[V]) prepareSearch() {
	i.blockPrefix = make([]uint64, len(i.blocks)+1)
	for blockIndex := range i.blocks {
		block := &i.blocks[blockIndex]
		i.blockPrefix[blockIndex+1] = i.blockPrefix[blockIndex] + block.bits.GetCardinality()
		// A one-value block's aggregate is identical to its only posting list.
		// Share the immutable bitmap instead of retaining two copies after Build.
		if len(block.items) == 1 {
			block.bits = block.items[0].bits
		}
		prepareBitmapForSearch(block.bits)
		for _, item := range block.items {
			if item.bits != block.bits {
				prepareBitmapForSearch(item.bits)
			}
		}
	}
}

// estimateCardinality returns the exact number of IDs visited by walk without
// traversing every aggregate bitmap in a wide range. Posting lists for distinct
// values in one ordered index are disjoint, so build-time block prefix sums can
// be combined with the at-most-one unaggregated boundary block.
func (i *orderedIndex[V]) estimateCardinality(value V, ascending, inclusive bool) uint64 {
	if len(i.blocks) == 0 {
		return 0
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
		var n uint64
		for pos := lo; pos < len(block.items); pos++ {
			n += block.items[pos].bits.GetCardinality()
		}
		if len(i.blockPrefix) == len(i.blocks)+1 {
			n += i.blockPrefix[len(i.blocks)] - i.blockPrefix[blockIndex+1]
		} else {
			for pos := blockIndex + 1; pos < len(i.blocks); pos++ {
				n += i.blocks[pos].bits.GetCardinality()
			}
		}
		return n
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
	var n uint64
	for pos := 0; pos < lo; pos++ {
		n += block.items[pos].bits.GetCardinality()
	}
	if len(i.blockPrefix) == len(i.blocks)+1 {
		n += i.blockPrefix[blockIndex]
	} else {
		for pos := 0; pos < blockIndex; pos++ {
			n += i.blocks[pos].bits.GetCardinality()
		}
	}
	return n
}
func (i *orderedIndex[V]) internBitmaps(interner *bitmapInterner) {
	for blockIndex := range i.blocks {
		block := &i.blocks[blockIndex]
		interner.intern(&block.bits)
		for _, item := range block.items {
			interner.intern(&item.bits)
		}
	}
}
func (i *orderedIndex[V]) insert(value V, id uint32) {
	if len(i.blocks) != 0 {
		blockIndex := i.blockFor(value)
		block := &i.blocks[blockIndex]
		pos := i.searchBlock(block, value)
		if pos < len(block.items) && i.compare(block.items[pos].value, value) == 0 {
			block.items[pos].bits.Add(id)
			block.bits.Add(id)
			return
		}
	}
	item := &orderedItem[V]{value: value, bits: roaring.New()}
	i.insertItem(item)
	item.bits.Add(id)
	block := i.blockFor(value)
	i.blocks[block].bits.Add(id)
}

func (i *orderedIndex[V]) searchBlock(block *orderedBlock[V], value V) int {
	lo, hi := 0, len(block.items)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if i.compare(block.items[mid].value, value) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (i *orderedIndex[V]) blockFor(value V) int {
	lo, hi := 0, len(i.blocks)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		last := i.blocks[mid].items[len(i.blocks[mid].items)-1]
		if i.compare(last.value, value) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(i.blocks) {
		return len(i.blocks) - 1
	}
	return lo
}

func (i *orderedIndex[V]) insertItem(item *orderedItem[V]) {
	if len(i.blocks) == 0 {
		items := make([]*orderedItem[V], 1, max(1, i.firstBlockCapacity))
		items[0] = item
		i.blocks = append(i.blocks, orderedBlock[V]{items: items, bits: roaring.New()})
		return
	}
	blockIndex := i.blockFor(item.value)
	block := &i.blocks[blockIndex]
	pos := i.searchBlock(block, item.value)
	block.items = append(block.items, nil)
	copy(block.items[pos+1:], block.items[pos:])
	block.items[pos] = item
	if len(block.items) <= orderedBlockSize*2 {
		return
	}
	rightItems := append([]*orderedItem[V](nil), block.items[orderedBlockSize:]...)
	block.items = block.items[:orderedBlockSize]
	right := orderedBlock[V]{items: rightItems, bits: roaring.New()}
	block.bits.Clear()
	for _, entry := range block.items {
		block.bits.Or(entry.bits)
	}
	for _, entry := range right.items {
		right.bits.Or(entry.bits)
	}
	i.blocks = append(i.blocks, orderedBlock[V]{})
	copy(i.blocks[blockIndex+2:], i.blocks[blockIndex+1:])
	i.blocks[blockIndex+1] = right
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
		for n := blockIndex + 1; n < len(i.blocks); n++ {
			visit(i.blocks[n].bits)
		}
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
	for n := blockIndex - 1; n >= 0; n-- {
		visit(i.blocks[n].bits)
	}
}

func (i *orderedIndex[V]) matches(value V, ascending, inclusive bool, id uint32) bool {
	found := false
	i.walk(value, ascending, inclusive, func(bits *roaring.Bitmap) {
		if !found && bits.Contains(id) {
			found = true
		}
	})
	return found
}
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
