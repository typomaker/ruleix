package ruleix

import (
	"time"

	"github.com/RoaringBitmap/roaring/v2"
)

type orderedItem[V any] struct {
	value V
	bits  *roaring.Bitmap
}

type orderedIndex[V any] struct {
	compare            Compare[V]
	blocks             []orderedBlock[V]
	blockPrefix        []uint64
	rangeBlocks        []orderedRangeBlock
	firstBlockCapacity int
	routing            orderedRouting
}

// orderedRouting maps an observed-domain monotonic key directly to the
// logical block that contains it. The parameters are learned from the values
// already collected by Build; the input iterator is never replayed.
type orderedRouting struct {
	min, width uint64
	blocks     []int
}

// orderedBlockSize balances aggregate count against the unaggregated boundary
// fragments. Production-shaped range searches are faster at 64 than at 128;
// smaller blocks add enough aggregate unions and retained bitmaps to regress.
const orderedBlockSize = 64

// orderedRangeBlockSize bounds the extra retained bitmap data to one more
// copy of each posting while reducing wide operator ranges to one visit per
// eight ordinary blocks. CompareBy enables this second level selectively;
// standalone ordered rules and Between keep their lower-memory layout.
const orderedRangeBlockSize = 8

type orderedBlock[V any] struct {
	items []*orderedItem[V]
	bits  *roaring.Bitmap
}

type orderedRangeBlock struct {
	first int
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
	i.prepareRouting()
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

func (i *orderedIndex[V]) prepareRangeSearch() {
	if len(i.blocks) < orderedRangeBlockSize*2 {
		return
	}
	i.rangeBlocks = make([]orderedRangeBlock, 0, len(i.blocks)/orderedRangeBlockSize)
	for first := 0; first+orderedRangeBlockSize <= len(i.blocks); first += orderedRangeBlockSize {
		bits := roaring.New()
		for block := first; block < first+orderedRangeBlockSize; block++ {
			bits.Or(i.blocks[block].bits)
		}
		prepareBitmapForSearch(bits)
		i.rangeBlocks = append(i.rangeBlocks, orderedRangeBlock{first: first, bits: bits})
	}
}

func (i *orderedIndex[V]) prepareRouting() {
	if len(i.blocks) == 0 {
		return
	}
	firstKey, ok := orderedRoutingKey(i.blocks[0].items[0].value)
	if !ok {
		return
	}
	lastBlock := &i.blocks[len(i.blocks)-1]
	lastKey, ok := orderedRoutingKey(lastBlock.items[len(lastBlock.items)-1].value)
	if !ok || lastKey < firstKey {
		return
	}

	// Keep the proven physical block layout. The logical intervals are only a
	// routing table, so enabling them does not duplicate or reshape postings.
	count := uint64(len(i.blocks))
	span := lastKey - firstKey
	width := span/count + 1
	if width == 0 {
		return
	}
	used := min(span/width+1, count)
	if used < 2 {
		return
	}

	routes := make([]int, used)
	block := 0
	for bucket := range routes {
		lower := firstKey + uint64(bucket)*width
		for block < len(i.blocks)-1 {
			last := i.blocks[block].items[len(i.blocks[block].items)-1]
			key, supported := orderedRoutingKey(last.value)
			if !supported || key >= lower {
				break
			}
			block++
		}
		routes[bucket] = block
	}
	i.routing = orderedRouting{min: firstKey, width: width, blocks: routes}
}

func orderedRoutingKey[V any](value V) (uint64, bool) {
	if key, ok := orderedScalarKey(any(value)); ok {
		return key, true
	}
	if instant, ok := any(value).(time.Time); ok {
		// Seconds cover the complete time.Time calendar range in int64. Values
		// within one second deliberately share a routing interval and remain
		// distinguished by the exact comparator in the boundary block.
		return signedOrderedKey(instant.Unix(), 64), true
	}
	return 0, false
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
	if len(i.routing.blocks) != 0 {
		if key, ok := orderedRoutingKey(value); ok {
			return i.routedBlockFor(value, key)
		}
	}
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

func (i *orderedIndex[V]) routedBlockFor(value V, key uint64) int {
	block := 0
	if key > i.routing.min {
		bucket := (key - i.routing.min) / i.routing.width
		if bucket >= uint64(len(i.routing.blocks)) {
			block = len(i.blocks) - 1
		} else {
			block = i.routing.blocks[bucket]
		}
	}
	for block > 0 {
		previous := i.blocks[block-1].items[len(i.blocks[block-1].items)-1]
		if i.compare(previous.value, value) < 0 {
			break
		}
		block--
	}
	for block < len(i.blocks)-1 {
		last := i.blocks[block].items[len(i.blocks[block].items)-1]
		if i.compare(last.value, value) >= 0 {
			break
		}
		block++
	}
	return block
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
