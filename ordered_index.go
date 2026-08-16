package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type orderedItem[V any] struct {
	value V
	bits  *roaring.Bitmap
}

type orderedIndex[V any] struct {
	compare Compare[V]
	blocks  []orderedBlock[V]
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
func (i *orderedIndex[V]) buildStatistics() orderedBuildStatistics {
	statistics := orderedBuildStatistics{blocks: len(i.blocks)}
	for block := range i.blocks {
		statistics.uniqueValues += len(i.blocks[block].items)
	}
	return statistics
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
		i.blocks = append(i.blocks, orderedBlock[V]{items: []*orderedItem[V]{item}, bits: roaring.New()})
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
