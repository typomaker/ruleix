package ruleix

import (
	"cmp"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

func TestOrderedIndexWideWalkUsesBlockAggregates(t *testing.T) {
	index := newOrderedIndex(cmp.Compare[int])
	for value := 0; value < 10_000; value++ {
		index.insert(value, uint32(value))
	}
	visits := 0
	index.walk(0, true, true, func(_ *roaring.Bitmap) { visits++ })
	if visits >= 200 {
		t.Fatalf("wide walk visited %d bitmaps for 10,000 unique values", visits)
	}
}

func TestOrderedIndexFindsValuesAfterUnorderedInsertAndBlockSplits(t *testing.T) {
	index := newOrderedIndex(cmp.Compare[int])
	const values = 1_000
	for step := 0; step < values; step++ {
		// 37 is coprime to 1,000, so this visits every value in a deliberately
		// non-sorted order and forces insertions throughout existing blocks.
		value := step * 37 % values
		index.insert(value, uint32(value))
		index.insert(value, uint32(value))
	}
	for value := 0; value < values; value++ {
		bits := index.exact(value)
		if bits == nil || bits.GetCardinality() != 1 || !bits.Contains(uint32(value)) {
			t.Fatalf("exact(%d) = %v", value, bits)
		}
	}
	if bits := index.exact(-1); bits != nil {
		t.Fatalf("exact(-1) = %v, want nil", bits)
	}
	if bits := index.exact(values); bits != nil {
		t.Fatalf("exact(%d) = %v, want nil", values, bits)
	}
}
