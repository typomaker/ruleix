package ruleix

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

type directIDBenchmarkConstraint struct {
	group       int
	value       int
	secondValue int
	thirdValue  int
	valueSet    bool
	secondSet   bool
	thirdSet    bool
}

// bitmapOnlyBenchmarkRule preserves the production rule's estimates and
// materialization while hiding its direct-ID and candidate-filter operations.
type bitmapOnlyBenchmarkRule[T any] struct{ child Rule[T] }

func (*bitmapOnlyBenchmarkRule[T]) rule() {}
func (r *bitmapOnlyBenchmarkRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &bitmapOnlyBenchmarkRule[T]{child: r.child.newState(ids, hints)}
}
func (r *bitmapOnlyBenchmarkRule[T]) validate(v T) error    { return r.child.validate(v) }
func (r *bitmapOnlyBenchmarkRule[T]) insert(v T, id uint32) { r.child.insert(v, id) }
func (r *bitmapOnlyBenchmarkRule[T]) cardinality(v T, pool *bitmapPool) uint64 {
	return r.child.cardinality(v, pool)
}
func (r *bitmapOnlyBenchmarkRule[T]) estimateCardinality(v T) uint64 {
	return r.child.(cardinalityEstimator[T]).estimateCardinality(v)
}
func (r *bitmapOnlyBenchmarkRule[T]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.search(v, dst, pool)
}
func (r *bitmapOnlyBenchmarkRule[T]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.exclude(v, dst, pool)
}
func (r *bitmapOnlyBenchmarkRule[T]) collectBuildStatistics(stats []nodeBuildStatistics) {
	r.child.collectBuildStatistics(stats)
}
func (r *bitmapOnlyBenchmarkRule[T]) prepareSearch() { prepareRuleSearch(r.child) }

// BenchmarkAllDirectIDFiltering compares cardinality-gated direct-ID checks
// with the previous materialize-and-intersect path for one and three equality
// constraints. Apple M1 Max / Go 1.26.0 medians from three 500 ms runs:
// Adaptive versus Bitmap was 634 ns/4.57 us and 815 ns/23.6 us at 8 candidates,
// 3.71/9.55 us and 5.13/29.3 us at 128 candidates, and 8.87/21.0 us and
// 38.3/44.0 us at 4,096 candidates (one/three constraints respectively).
//
//	go test -run '^$' -bench '^BenchmarkAllDirectIDFiltering/' -benchmem -benchtime=500ms -count=3 .
func BenchmarkAllDirectIDFiltering(b *testing.B) {
	const entries = 100_000
	constraints := make([]directIDBenchmarkConstraint, entries)
	ids := make([]uint32, entries)
	for id := range uint32(entries) {
		constraints[id] = directIDBenchmarkConstraint{
			group: int(id), value: int(id % 2), secondValue: int(id % 3), thirdValue: int(id % 5),
			valueSet: id%7 != 0, secondSet: id%11 != 0, thirdSet: id%13 != 0,
		}
		ids[id] = id
	}
	for _, candidates := range []int{8, 128, 4_096} {
		for _, direct := range []int{1, 3} {
			for _, mode := range []string{"Adaptive", "Bitmap"} {
				name := fmt.Sprintf("Candidates%d/Direct%d/%s", candidates, direct, mode)
				b.Run(name, func(b *testing.B) {
					rules := make([]Rule[directIDBenchmarkConstraint], 1, direct+1)
					rules[0] = Include(func(value directIDBenchmarkConstraint) (int, bool) {
						return value.group / candidates, true
					})
					getters := []Getter[directIDBenchmarkConstraint, int]{
						func(value directIDBenchmarkConstraint) (int, bool) { return value.value, value.valueSet },
						func(value directIDBenchmarkConstraint) (int, bool) { return value.secondValue, value.secondSet },
						func(value directIDBenchmarkConstraint) (int, bool) { return value.thirdValue, value.thirdSet },
					}
					for i := range direct {
						child := Include(getters[i])
						if mode == "Bitmap" {
							child = &bitmapOnlyBenchmarkRule[directIDBenchmarkConstraint]{child: child}
						}
						rules = append(rules, child)
					}
					index, err := New[directIDBenchmarkConstraint, uint32](All(rules...)).Build(Zip(constraints, ids))
					requireNoBenchmarkError(b, err)
					query := directIDBenchmarkConstraint{
						group: 0, valueSet: true, secondSet: true, thirdSet: true,
					}
					dst := roaring.New()
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						dst.Clear()
						index.root.search(query, dst, index.pool)
						plannerBenchmarkCardinality = dst.GetCardinality()
					}
				})
			}
		}
	}
}
