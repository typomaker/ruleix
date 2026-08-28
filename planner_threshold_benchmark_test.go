package ruleix

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

const plannerBenchmarkUniverse = 1 << 20

var plannerBenchmarkCardinality uint64

type inaccurateEstimateRule struct {
	*countingRule
	estimate uint64
}

func (r *inaccurateEstimateRule) estimateCardinality(int) uint64      { return r.estimate }
func (r *inaccurateEstimateRule) estimateCheapCardinality(int) uint64 { return r.estimate }

type unknownEstimateMatchingRule[T any] struct{ child Rule[T] }

type estimatedMatchingRule[T any] struct {
	*unknownEstimateMatchingRule[T]
	estimate uint64
}

func (r *estimatedMatchingRule[T]) estimateCardinality(T) uint64      { return r.estimate }
func (r *estimatedMatchingRule[T]) estimateCheapCardinality(T) uint64 { return r.estimate }

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

// BenchmarkAllCostBasedBroadSibling covers the first production cost-model
// decision that intentionally scans beyond allCandidateScanLimit: 16 exact
// candidates are cheaper to validate than intersecting a 100K-ID sibling.
// Last local run on Apple M1 Max: cost model 285.5 ns/op, 32 B/op, 2 allocs;
// threshold fallback 25.7 us/op, 135,910 B/op, 53 allocs (median of five 1 s runs).
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllCostBasedBroadSibling$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllCostBasedBroadSibling(b *testing.B) {
	selective := roaring.New()
	selective.AddRange(0, 16)
	broad := roaring.New()
	for id := uint32(0); id < 1_000_000; id += 10 {
		broad.Add(id)
	}
	for name, children := range map[string][]Rule[int]{
		"CostModel": {&matchAllRule[int]{bits: selective}, &matchAllRule[int]{bits: broad}},
		"ThresholdFallback": {
			&unknownEstimateRule[int]{child: &matchAllRule[int]{bits: selective}},
			&unknownEstimateRule[int]{child: &matchAllRule[int]{bits: broad}},
		},
	} {
		b.Run(name, func(b *testing.B) {
			rule := &allRule[int]{children: children}
			rule.prepareSearch()
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

// BenchmarkAllInitialSourceTotalCost isolates the initial-source decision for
// uncached Index execution. The narrower source is expensive to materialize,
// while the slightly broader dense posting is cheap to clone before the same
// exact narrowing work. Last local run on Apple M1 Max: total cost 2.439 us/op,
// 12,393 B/op, 8 allocs; cardinality order 3.919 us/op, 20,612 B/op,
// 10 allocs (median of five 1 s runs). Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllInitialSourceTotalCost$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllInitialSourceTotalCost(b *testing.B) {
	narrow := roaring.New()
	for id := uint32(0); id < 4_000; id++ {
		narrow.Add(id * 32)
	}
	broad := roaring.New()
	broad.AddRange(0, 4_096)
	narrowRule := &estimatedMatchingRule[int]{
		unknownEstimateMatchingRule: &unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: narrow}},
		estimate:                    narrow.GetCardinality(),
	}
	for name, broadRule := range map[string]Rule[int]{
		"TotalCost":        &matchAllRule[int]{bits: broad},
		"CardinalityOrder": &unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: broad}},
	} {
		b.Run(name, func(b *testing.B) {
			rule := &allRule[int]{children: []Rule[int]{narrowRule, broadRule}}
			rule.prepareSearch()
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

// BenchmarkAllEstimatedBroadSibling covers direct validation against an
// unmaterialized child whose complete result has a conservative estimate.
// Last local run on Apple M1 Max: cost model 542.9 ns/op, 64 B/op, 4 allocs;
// unknown-cost fallback 20.844 us/op, 135,878 B/op, 51 allocs (median of five
// 1 s runs).
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllEstimatedBroadSibling$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllEstimatedBroadSibling(b *testing.B) {
	selective := roaring.New()
	selective.AddRange(0, 16)
	broad := roaring.New()
	for id := uint32(0); id < 1_000_000; id += 10 {
		broad.Add(id)
	}
	for name, sibling := range map[string]Rule[int]{
		"CostModel": &estimatedMatchingRule[int]{
			unknownEstimateMatchingRule: &unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: broad}},
			estimate:                    broad.GetCardinality(),
		},
		"UnknownFallback": &unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: broad}},
	} {
		b.Run(name, func(b *testing.B) {
			rule := &allRule[int]{children: []Rule[int]{&matchAllRule[int]{bits: selective}, sibling}}
			rule.prepareSearch()
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

// BenchmarkAllLateMaterializedCandidateFallback covers a small result found
// after a broad child has already been materialized. The executor can validate
// that earlier child from its bitmap instead of invoking its rule matcher.
func BenchmarkAllLateMaterializedCandidateFallback(b *testing.B) {
	broad := roaring.New()
	broad.AddRange(0, 100_000)
	selective := roaring.BitmapOf(7)
	rule := &allRule[int]{children: []Rule[int]{
		&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: broad}},
		&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: selective}},
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
}

// BenchmarkAllReplanAfterIntersection covers inaccurate 16-ID estimates whose
// measured overlap has only two IDs. The executor replans at that boundary and
// validates the remaining 100K-ID posting instead of materializing it.
// Last local run on Apple M1 Max: 413.8 ns/op, 144 B/op, 10 allocs/op
// (median of five 1 s runs). Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllReplanAfterIntersection$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllReplanAfterIntersection(b *testing.B) {
	first := roaring.New()
	first.AddRange(0, 16)
	second := roaring.New()
	second.AddRange(14, 30)
	broad := roaring.New()
	broad.AddRange(0, 100_000)
	rule := &allRule[int]{children: []Rule[int]{
		&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: first}},
		&unknownEstimateMatchingRule[int]{child: &matchAllRule[int]{bits: second}},
		&matchAllRule[int]{bits: broad},
	}}
	rule.prepareSearch()
	pool := newBitmapPool()
	dst := roaring.New()
	b.ReportAllocs()
	for range b.N {
		dst.Clear()
		rule.search(0, dst, pool)
	}
	plannerBenchmarkCardinality = dst.GetCardinality()
}

// BenchmarkAllCandidateValidationOrder covers a cheaper bitmap check that is
// less selective than a representation matcher. Rejection-per-cost ordering
// runs the bitmap first; the comparison hides that bitmap and therefore keeps
// cardinality order. Last local run on Apple M1 Max: cost order 1.035 us/op,
// 80 B/op, 5 allocs; cardinality order 2.436 us/op, 80 B/op, 5 allocs (median
// of five 1 s runs). Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllCandidateValidationOrder$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllCandidateValidationOrder(b *testing.B) {
	source := &inaccurateEstimateRule{countingRule: &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5, 6, 7}}, estimate: 1}
	expensiveIDs := []uint32{0, 1}
	for id := uint32(1_000); id < 2_000; id++ {
		expensiveIDs = append(expensiveIDs, id)
	}
	expensive := &inaccurateEstimateRule{countingRule: &countingRule{ids: expensiveIDs}, estimate: 2}
	cheap := &countingRule{ids: []uint32{0, 1, 2, 3}}
	for name, cheapRule := range map[string]Rule[int]{
		"CostOrder":        &planningCountingRule{countingRule: cheap, bits: roaring.BitmapOf(0, 1, 2, 3)},
		"CardinalityOrder": cheap,
	} {
		b.Run(name, func(b *testing.B) {
			rule := &allRule[int]{children: []Rule[int]{source, expensive, cheapRule}}
			rule.prepareSearch()
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
