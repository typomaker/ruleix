package ruleix

import (
	"cmp"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

type streamingOrderedFixture struct{ value int }

func streamingOrderedValue(v streamingOrderedFixture) (int, bool) { return v.value, true }

func TestLossyOrderedStreamingRegridsExpandedRange(t *testing.T) {
	minimum, _ := orderedScalarKey(any(10))
	middle, _ := orderedScalarKey(any(19))
	rule := &lossyOrderedRule[streamingOrderedFixture, int]{
		get: streamingOrderedValue, dir: greaterThan, inclusive: true,
		min: minimum, max: middle, width: 5,
		buckets:  []*roaring.Bitmap{roaring.BitmapOf(0), roaring.BitmapOf(1)},
		wildcard: roaring.New(),
	}
	rule.insert(streamingOrderedFixture{value: 30}, 2)
	if len(rule.buckets) != 2 {
		t.Fatalf("expanded grid has %d buckets, want 2", len(rule.buckets))
	}
	for id, stored := range []int{10, 19, 30} {
		for query := stored; query <= 35; query++ {
			if !rule.matchesID(streamingOrderedFixture{value: query}, uint32(id)) {
				t.Fatalf("stored %d disappeared for query %d", stored, query)
			}
		}
	}
}

func TestLossyComparedStreamingRegridsBothEdges(t *testing.T) {
	buckets := lossyComparedBuckets[int]{
		compare: cmp.Compare[int], minimum: 10, maximum: 30, capacity: 3,
		boundaries: []int{10, 20, 30},
		buckets:    []*roaring.Bitmap{roaring.BitmapOf(0), roaring.BitmapOf(1), roaring.BitmapOf(2)},
	}
	buckets.insert(40, 3)
	buckets.insert(0, 4)
	if len(buckets.buckets) != 3 {
		t.Fatalf("expanded comparator grid has %d buckets, want 3", len(buckets.buckets))
	}
	for id, stored := range []int{10, 20, 30, 40, 0} {
		first, last, ok := buckets.matchingRange(stored, greaterThan, true)
		if !ok || !buckets.rangeContains(first, last, uint32(id)) {
			t.Fatalf("stored %d disappeared after comparator rebucketing", stored)
		}
	}
}

func TestLossyComparedStreamingFitsGradually(t *testing.T) {
	buckets := lossyComparedBuckets[int]{
		compare: cmp.Compare[int], minimum: 10, maximum: 40, capacity: 4,
		boundaries: []int{10, 20, 30, 40},
		buckets: []*roaring.Bitmap{
			roaring.BitmapOf(0), roaring.BitmapOf(1), roaring.BitmapOf(2), roaring.BitmapOf(3),
		},
	}
	before := buckets.memoryUsage()
	buckets.coarsenOne()
	if len(buckets.buckets) != 3 {
		t.Fatalf("one pressure step has %d buckets, want 3", len(buckets.buckets))
	}
	if after := buckets.memoryUsage(); after >= before {
		t.Fatalf("one pressure step used %d bytes, want less than %d", after, before)
	}
}

func TestLossyEqualityStreamingRebucketsWithoutDroppingIDs(t *testing.T) {
	rule := &lossyEqualityRule[streamingOrderedFixture, int]{
		bucketCount: 4,
		buckets: map[uint64]lossyEqualityPosting{
			0: {bits: roaring.BitmapOf(0)},
			1: {bits: roaring.BitmapOf(1)},
			2: {bits: roaring.BitmapOf(2)},
			3: {bits: roaring.BitmapOf(3)},
		},
	}
	rule.rebucket(3)
	if rule.bucketCount != 3 {
		t.Fatalf("bucket count is %d, want 3", rule.bucketCount)
	}
	seen := roaring.New()
	for _, posting := range rule.buckets {
		seen.Or(posting.bits)
	}
	if seen.GetCardinality() != 4 {
		t.Fatalf("rebucketing retained %d IDs, want 4", seen.GetCardinality())
	}
}
