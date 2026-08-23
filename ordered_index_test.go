package ruleix

import (
	"cmp"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
)

func TestOrderedIndexBuildsLogicalRoutingForNumbersAndTime(t *testing.T) {
	t.Run("numbers", func(t *testing.T) {
		index := newOrderedIndex(cmp.Compare[int])
		for value := range 1_000 {
			index.insert(value, uint32(value))
		}
		index.prepareSearch()
		if len(index.routing.blocks) == 0 {
			t.Fatal("numeric routing was not built")
		}
		for _, value := range []int{-1, 0, 127, 500, 999, 1_000} {
			block := index.blockFor(value)
			if block < 0 || block >= len(index.blocks) {
				t.Fatalf("blockFor(%d) = %d", value, block)
			}
		}
	})

	t.Run("time", func(t *testing.T) {
		index := newOrderedIndex(time.Time.Compare)
		base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
		for hour := range 24 * 30 {
			index.insert(base.Add(time.Duration(hour)*time.Hour), uint32(hour))
		}
		index.prepareSearch()
		if len(index.routing.blocks) == 0 {
			t.Fatal("time routing was not built")
		}
		for _, hour := range []int{-1, 0, 12, 300, 719, 720} {
			query := base.Add(time.Duration(hour) * time.Hour)
			bits := index.exact(query)
			if hour >= 0 && hour < 720 {
				if bits == nil || !bits.Contains(uint32(hour)) {
					t.Fatalf("exact hour %d = %v", hour, bits)
				}
			} else if bits != nil {
				t.Fatalf("exact hour %d = %v, want nil", hour, bits)
			}
		}
	})
}

var orderedIndexBenchmarkResult uint64
var orderedIndexBlockBenchmarkResult int

func TestOrderedIndexCardinalityEstimateMatchesWalk(t *testing.T) {
	index := newOrderedIndex(cmp.Compare[int])
	for value := 0; value < 1_000; value++ {
		for duplicate := 0; duplicate <= value%3; duplicate++ {
			index.insert(value, uint32(value*3+duplicate))
		}
	}
	index.prepareSearch()

	for _, value := range []int{-1, 0, 1, 127, 128, 255, 500, 999, 1_000} {
		for _, ascending := range []bool{false, true} {
			for _, inclusive := range []bool{false, true} {
				var walked uint64
				index.walk(value, ascending, inclusive, func(bits *roaring.Bitmap) {
					walked += bits.GetCardinality()
				})
				estimated := index.estimateCardinality(value, ascending, inclusive)
				if estimated != walked {
					t.Fatalf("estimate(%d, ascending=%t, inclusive=%t) = %d, want %d",
						value, ascending, inclusive, estimated, walked)
				}
			}
		}
	}
}

func BenchmarkOrderedIndexCardinalityEstimate(b *testing.B) {
	index := newOrderedIndex(cmp.Compare[int])
	for value := 0; value < 10_000; value++ {
		index.insert(value, uint32(value))
	}
	index.prepareSearch()
	b.Run("BlockPrefix", func(b *testing.B) {
		for range b.N {
			orderedIndexBenchmarkResult = index.estimateCardinality(5_000, true, true)
		}
	})
	b.Run("WalkAggregates", func(b *testing.B) {
		for range b.N {
			var cardinality uint64
			index.walk(5_000, true, true, func(bits *roaring.Bitmap) {
				cardinality += bits.GetCardinality()
			})
			orderedIndexBenchmarkResult = cardinality
		}
	})
}

func BenchmarkOrderedIndexTimeBlockLookup(b *testing.B) {
	index := newOrderedIndex(time.Time.Compare)
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for minute := range 10_000 {
		index.insert(base.Add(time.Duration(minute)*time.Minute), uint32(minute))
	}
	index.prepareSearch()
	query := base.Add(5_000 * time.Minute)

	b.Run("LogicalRouting", func(b *testing.B) {
		for range b.N {
			orderedIndexBlockBenchmarkResult = index.blockFor(query)
		}
	})
	index.routing = orderedRouting{}
	b.Run("ComparatorBinarySearch", func(b *testing.B) {
		for range b.N {
			orderedIndexBlockBenchmarkResult = index.blockFor(query)
		}
	})
}

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

func TestOrderedIndexSharesSingleItemBlockBitmapAfterBuild(t *testing.T) {
	index := newOrderedIndex(cmp.Compare[int])
	for id := range uint32(100) {
		index.insert(1, id)
	}

	index.prepareSearch()
	block := &index.blocks[0]
	if block.bits != block.items[0].bits {
		t.Fatal("single-item block retained a duplicate aggregate bitmap")
	}

	visits := 0
	index.walk(1, false, true, func(bits *roaring.Bitmap) {
		visits++
		if bits.GetCardinality() != 100 {
			t.Fatalf("cardinality = %d, want 100", bits.GetCardinality())
		}
	})
	if visits != 1 {
		t.Fatalf("walk visited %d bitmaps, want 1", visits)
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
	index.prepareSearch()
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
