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
