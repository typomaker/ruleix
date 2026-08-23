package ruleix

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

const plannerBenchmarkUniverse = 1 << 20

var plannerBenchmarkCardinality uint64

type unknownEstimateMatchingRule[T any] struct{ child Rule[T] }

func (*unknownEstimateMatchingRule[T]) rule() {}
func (r *unknownEstimateMatchingRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &unknownEstimateMatchingRule[T]{child: r.child.newState(ids, hints)}
}
func (r *unknownEstimateMatchingRule[T]) validate(v T) error { return r.child.validate(v) }
func (r *unknownEstimateMatchingRule[T]) insert(v T, id uint32) {
	r.child.insert(v, id)
}
func (r *unknownEstimateMatchingRule[T]) cardinality(v T, pool *bitmapPool) uint64 {
	return r.child.cardinality(v, pool)
}
func (r *unknownEstimateMatchingRule[T]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.search(v, dst, pool)
}
func (r *unknownEstimateMatchingRule[T]) matchesID(v T, id uint32) bool {
	return r.child.(ruleIDMatcher[T]).matchesID(v, id)
}
func (r *unknownEstimateMatchingRule[T]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.exclude(v, dst, pool)
}
func (r *unknownEstimateMatchingRule[T]) collectBuildStatistics(stats []nodeBuildStatistics) {
	r.child.collectBuildStatistics(stats)
}

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

// BenchmarkNestedAllEstimate isolates the outer planner decision enabled by
// propagating the cheapest known estimate through a nested All. The broad
// sibling intentionally comes first in schema order.
func BenchmarkNestedAllEstimate(b *testing.B) {
	broad := roaring.New()
	broad.AddRange(0, 100_000)
	selective := roaring.BitmapOf(7)
	nested := &allRule[int]{children: []Rule[int]{
		&matchAllRule[int]{bits: broad},
		&matchAllRule[int]{bits: selective},
	}}
	for name, candidate := range map[string]Rule[int]{
		"Adaptive":        nested,
		"UnknownEstimate": &unknownEstimateRule[int]{child: nested},
	} {
		b.Run(name, func(b *testing.B) {
			rule := &allRule[int]{children: []Rule[int]{
				&matchAllRule[int]{bits: broad},
				candidate,
			}}
			pool := newBitmapPool()
			dst := roaring.New()
			b.ReportAllocs()
			for range b.N {
				dst.Clear()
				rule.search(0, dst, pool)
			}
			plannerBenchmarkCardinality = dst.GetCardinality()
		})
	}
}

// BenchmarkAllMaterializedCandidateFallback measures an All whose first child
// cannot expose a cheap estimate but materializes only a few IDs. The remaining
// broad children make avoiding their bitmap materialization visible.
func BenchmarkAllMaterializedCandidateFallback(b *testing.B) {
	broad := roaring.New()
	broad.AddRange(0, 100_000)
	for _, cardinality := range []uint64{1, allCandidateScanLimit} {
		b.Run(fmt.Sprintf("Cardinality%d", cardinality), func(b *testing.B) {
			selective := roaring.New()
			selective.AddRange(0, cardinality)
			rule := &allRule[int]{children: []Rule[int]{
				&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: selective}},
				&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: broad}},
				&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: broad}},
				&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: broad}},
			}}
			pool := newBitmapPool()
			dst := roaring.New()
			b.ReportAllocs()
			for range b.N {
				dst.Clear()
				rule.search(0, dst, pool)
			}
			plannerBenchmarkCardinality = dst.GetCardinality()
		})
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
