package ruleix

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

const plannerBenchmarkUniverse = 1 << 20

var plannerBenchmarkCardinality uint64

// BenchmarkAllExecutionThreshold isolates the work controlled by
// allCandidateScanLimit. Candidate scans iterate the smallest posting and
// validate IDs in the remaining postings; bitmap execution clones that posting
// and intersects the rest. Dense and sparse IDs exercise different Roaring
// containers, while 2, 4, and 8 children cover typical and worst-case All
// widths without mixing build or result-materialization costs into the signal.
//
//nolint:gocognit // The matrix deliberately keeps all execution modes and shapes together.
func BenchmarkAllExecutionThreshold(b *testing.B) {
	for _, children := range []int{2, 4, 8} {
		for _, cardinality := range []uint64{1, 4, 16, 64, 256, 1 << 10, 4 << 10, 16 << 10} {
			for _, shape := range []string{"Dense", "Sparse"} {
				postings := plannerBenchmarkPostings(children, cardinality, shape == "Sparse")
				name := fmt.Sprintf("Children%d/Cardinality%d/%s", children, cardinality, shape)
				b.Run(name, func(b *testing.B) {
					b.Run("Candidate", func(b *testing.B) {
						b.ReportAllocs()
						for range b.N {
							plannerBenchmarkCardinality = plannerCandidateCardinality(postings)
						}
					})
					b.Run("Bitmap", func(b *testing.B) {
						scratch := plannerBenchmarkScratch(postings)
						b.ReportAllocs()
						for range b.N {
							plannerBenchmarkCardinality = plannerBitmapCardinality(postings, scratch)
						}
					})
					b.Run("Adaptive", func(b *testing.B) {
						scratch := plannerBenchmarkScratch(postings)
						b.ReportAllocs()
						for range b.N {
							if cardinality <= allCandidateScanLimit {
								plannerBenchmarkCardinality = plannerCandidateCardinality(postings)
							} else {
								plannerBenchmarkCardinality = plannerBitmapCardinality(postings, scratch)
							}
						}
					})
				})
			}
		}
	}
}

func plannerBenchmarkPostings(children int, cardinality uint64, sparse bool) []*roaring.Bitmap {
	postings := make([]*roaring.Bitmap, children)
	step := uint64(1)
	if sparse {
		step = plannerBenchmarkUniverse / cardinality
	}
	for child := range postings {
		bits := roaring.New()
		for id := uint64(0); id < cardinality; id++ {
			bits.Add(uint32(id * step))
		}
		postings[child] = bits
	}
	return postings
}

func plannerCandidateCardinality(postings []*roaring.Bitmap) uint64 {
	var matches uint64
	iterator := postings[0].Iterator()
	for iterator.HasNext() {
		id := iterator.Next()
		matched := true
		for _, posting := range postings[1:] {
			if !posting.Contains(id) {
				matched = false
				break
			}
		}
		if matched {
			matches++
		}
	}
	return matches
}

func plannerBenchmarkScratch(postings []*roaring.Bitmap) []*roaring.Bitmap {
	scratch := make([]*roaring.Bitmap, len(postings))
	for i := range scratch {
		scratch[i] = roaring.New()
	}
	return scratch
}

func plannerBitmapCardinality(postings, scratch []*roaring.Bitmap) uint64 {
	for i, posting := range postings {
		scratch[i].Clear()
		scratch[i].SetCopyOnWrite(true)
		scratch[i].Or(posting)
	}
	result := roaring.FastAnd(scratch...)
	return result.GetCardinality()
}
