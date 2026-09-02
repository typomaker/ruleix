package ruleix

import (
	"cmp"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestLossyComparedBucketsAggregateFullInteriorBlocks(t *testing.T) {
	const values = lossyComparedAggregateSize*2 + 17
	index := newOrderedIndex(cmp.Compare[int])
	for value := range values {
		index.insert(value, uint32(value))
	}
	buckets := buildLossyComparedBuckets(&index, values)
	require.Len(t, buckets.aggregates, 2)
	buckets.prepareSearch()

	first, last := 3, lossyComparedAggregateSize*2+7
	bits := roaring.New()
	buckets.addRange(first, last, bits)
	require.Equal(t, uint64(last-first+1), bits.GetCardinality())
	require.Equal(t, uint64(last-first+1), buckets.rangeCardinality(first, last))
	for id := uint32(0); id < values; id++ {
		require.Equal(t, id >= uint32(first) && id <= uint32(last), buckets.rangeContains(first, last, id))
	}
}

func TestLossyComparedBucketsKeepAggregatesInSyncAfterMutation(t *testing.T) {
	const values = lossyComparedAggregateSize + 1
	buckets := lossyComparedBuckets[int]{compare: cmp.Compare[int], capacity: values}
	for value := range values {
		buckets.insert(value, uint32(value))
	}
	require.Len(t, buckets.aggregates, 1)
	require.True(t, buckets.rangeContains(0, lossyComparedAggregateSize-1, 64))

	buckets.insert(64, 999)
	require.True(t, buckets.rangeContains(0, lossyComparedAggregateSize-1, 999))

	buckets.coarsenOne()
	wantAggregates := len(buckets.buckets) / lossyComparedAggregateSize
	require.Len(t, buckets.aggregates, wantAggregates)
	bits := roaring.New()
	buckets.addRange(0, len(buckets.buckets)-1, bits)
	require.Equal(t, uint64(values+1), bits.GetCardinality())
}

var lossyComparedAggregateBenchmarkBits *roaring.Bitmap

// Latest Apple M1 Max / Go 1.26.0 result with 1,024 leaves and a 989-leaf
// range, GOMAXPROCS=1, 500ms, count=5: leaf union median 269.6 us/op,
// 6,160 B/op, 11 allocs; aggregate union median 60.74 us/op, 3,856 B/op,
// 10 allocs. Reproduce with:
//
//	GOMAXPROCS=1 go test -run '^$' \
//	  -bench '^BenchmarkLossyComparedBucketWideRange/' \
//	  -benchmem -benchtime=500ms -count=5 .
func BenchmarkLossyComparedBucketWideRange(b *testing.B) {
	const values = lossyComparedAggregateSize * 8
	index := newOrderedIndex(cmp.Compare[int])
	for value := range values {
		index.insert(value, uint32(value))
	}
	buckets := buildLossyComparedBuckets(&index, values)
	first, last := 17, values-19
	for _, mode := range []string{"Leaves", "Aggregates"} {
		b.Run(mode, func(b *testing.B) {
			dst := roaring.New()
			b.ReportAllocs()
			for b.Loop() {
				dst.Clear()
				if mode == "Leaves" {
					for bucket := first; bucket <= last; bucket++ {
						dst.Or(buckets.buckets[bucket])
					}
				} else {
					buckets.addRange(first, last, dst)
				}
			}
			lossyComparedAggregateBenchmarkBits = dst
		})
	}
}
