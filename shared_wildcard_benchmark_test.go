package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

var sharedWildcardBenchmarkResult uint64

// BenchmarkSharedWildcardAll compares the current All execution (materialize
// W union Ai for every child) with the proposed shared-wildcard identity
// W union (A1 intersection ... intersection An). The postings deliberately
// model partial, rather than universal, wildcards so Build cannot optimize the
// equality children away.
//
//nolint:gocognit // The matrix keeps both execution strategies directly comparable.
func BenchmarkSharedWildcardAll(b *testing.B) {
	const universe = uint64(10_000)
	for _, wildcardPercent := range []uint64{25, 75} {
		for _, children := range []int{2, 4, 8} {
			name := fmt.Sprintf("Wildcard%dPercent/Children%d", wildcardPercent, children)
			wildcard, concrete := sharedWildcardPostings(universe, wildcardPercent, children)
			b.Run(name, func(b *testing.B) {
				b.Run("RepeatedUnion", func(b *testing.B) {
					scratch := make([]*roaring.Bitmap, children)
					for i := range scratch {
						scratch[i] = roaring.New()
					}
					b.ReportAllocs()
					for range b.N {
						for i := range scratch {
							scratch[i].Clear()
							scratch[i].Or(wildcard)
							scratch[i].Or(concrete[i])
						}
						result := scratch[0]
						for _, child := range scratch[1:] {
							result.And(child)
						}
						sharedWildcardBenchmarkResult = result.GetCardinality()
					}
				})
				b.Run("SharedUnion", func(b *testing.B) {
					result := roaring.New()
					b.ReportAllocs()
					for range b.N {
						result.Clear()
						result.Or(concrete[0])
						for _, child := range concrete[1:] {
							result.And(child)
						}
						result.Or(wildcard)
						sharedWildcardBenchmarkResult = result.GetCardinality()
					}
				})
			})
		}
	}
}

func sharedWildcardPostings(
	universe uint64,
	wildcardPercent uint64,
	children int,
) (*roaring.Bitmap, []*roaring.Bitmap) {
	wildcard := roaring.New()
	wildcard.AddRange(0, universe*wildcardPercent/100)
	concrete := make([]*roaring.Bitmap, children)
	for child := range concrete {
		concrete[child] = roaring.New()
		// Each queried value is selective, with a smaller common intersection.
		for id := universe * wildcardPercent / 100; id < universe; id++ {
			if (id+uint64(child))%64 == 0 || id%1024 == 0 {
				concrete[child].Add(uint32(id))
			}
		}
	}
	return wildcard, concrete
}
