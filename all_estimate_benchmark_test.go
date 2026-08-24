package ruleix

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

type allEstimateBenchmarkConstraint struct {
	value    int
	operator Operator
}

func BenchmarkAllEstimateRanking(b *testing.B) {
	for _, children := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("Children%d", children), func(b *testing.B) {
			rules := make([]Rule[allEstimateBenchmarkConstraint], children)
			for i := range rules {
				rules[i] = CompareBy(
					func(c allEstimateBenchmarkConstraint) (int, bool) { return c.value, true },
					func(c allEstimateBenchmarkConstraint) (Operator, bool) { return c.operator, true },
					cmp.Compare[int],
				)
			}
			constraints := make([]allEstimateBenchmarkConstraint, 10_000)
			ids := make([]uint32, len(constraints))
			for id := range uint32(10_000) {
				constraints[id] = allEstimateBenchmarkConstraint{
					value:    int(id % 1_000),
					operator: OperatorGTE,
				}
				ids[id] = id
			}
			index, err := New[allEstimateBenchmarkConstraint, uint32](All(rules...)).Build(Zip(constraints, ids))
			requireNoBenchmarkError(b, err)
			query := allEstimateBenchmarkConstraint{value: 999}
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

// BenchmarkAllOrderedCandidateFiltering measures a broad equality candidate
// followed by an uncached ordered range. Latest Apple M1 Max / Go 1.26.0
// medians: 133.0 us, 48,915 B, 16 allocs before candidate filtering; 53.1 us,
// 39,879 B, 11 allocs after. Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllOrderedCandidateFiltering$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllOrderedCandidateFiltering(b *testing.B) {
	type constraint struct {
		group int
		value int
	}
	const entries = 38_098
	constraints := make([]constraint, entries)
	ids := make([]uint32, entries)
	for id := range uint32(entries) {
		constraints[id] = constraint{group: int(id % 2), value: int(id)}
		ids[id] = id
	}
	rule := All(
		Include(func(value constraint) (int, bool) { return value.group, true }),
		GreaterOrEqual(func(value constraint) (int, bool) { return value.value, true }, cmp.Compare[int]),
	)
	index, err := New[constraint, uint32](rule).Build(Zip(constraints, ids))
	requireNoBenchmarkError(b, err)
	query := constraint{group: 0, value: entries / 2}
	dst := roaring.New()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dst.Clear()
		index.root.search(query, dst, index.pool)
		plannerBenchmarkCardinality = dst.GetCardinality()
	}
}

func requireNoBenchmarkError(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}
