package ruleix

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

var benchmarkAllEmptyResult uint64

// BenchmarkAllLeadingIntersection measures the empty-aware check added to the
// broad bitmap path. Disjoint reports the avoided child materializations;
// Overlap records the cost paid when execution must continue normally.
func BenchmarkAllLeadingIntersection(b *testing.B) {
	for _, children := range []int{4, 8} {
		b.Run(fmt.Sprintf("Children%d", children), func(b *testing.B) {
			for _, overlap := range []bool{false, true} {
				name := "Disjoint"
				if overlap {
					name = "Overlap"
				}
				b.Run(name, func(b *testing.B) {
					rules := make([]Rule[int], children)
					for i := range rules {
						start := uint64(0)
						if i == 1 && !overlap {
							start = 1 << 15
						}
						bits := roaring.New()
						bits.AddRange(start, start+(1<<14))
						rules[i] = newMatchAllRule[int](bits)
					}
					rule := All(rules...)
					pool := newBitmapPool()
					dst := roaring.New()
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						dst.Clear()
						rule.search(0, dst, pool)
						benchmarkAllEmptyResult = dst.GetCardinality()
					}
				})
			}
		})
	}
}

// BenchmarkAllLateRangePruning measures the hybrid executor when the first
// pair overlaps but the third ranked bitmap proves the result empty.
func BenchmarkAllLateRangePruning(b *testing.B) {
	rules := []Rule[int]{
		newMatchAllRule[int](benchmarkBitmapRange(0, 1<<14)),
		newMatchAllRule[int](benchmarkBitmapRange(1<<13, 1<<14)),
		newMatchAllRule[int](benchmarkBitmapRange(1<<15, 1<<14)),
		newMatchAllRule[int](benchmarkBitmapRange(0, 1<<16)),
	}
	rule := All(rules...)
	pool := newBitmapPool()
	dst := roaring.New()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dst.Clear()
		rule.search(0, dst, pool)
		benchmarkAllEmptyResult = dst.GetCardinality()
	}
}

func benchmarkBitmapRange(start, size uint64) *roaring.Bitmap {
	bits := roaring.New()
	bits.AddRange(start, start+size)
	return bits
}
