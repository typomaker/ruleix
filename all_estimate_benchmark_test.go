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

// BenchmarkAllCompareByCandidateFiltering measures a broad equality candidate
// followed by an uncached CompareBy union. Latest Apple M1 Max / Go 1.26.0
// medians: 59.5 us, 41,937 B, 13 allocs before candidate filtering; 47.0 us,
// 21,060 B, 7 allocs after. Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllCompareByCandidateFiltering$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllCompareByCandidateFiltering(b *testing.B) {
	type constraint struct {
		group    int
		value    int
		operator Operator
	}
	const entries = 38_098
	constraints := make([]constraint, entries)
	ids := make([]uint32, entries)
	for id := range uint32(entries) {
		constraints[id] = constraint{
			group: int(id % 2), value: int(id), operator: OperatorGTE,
		}
		ids[id] = id
	}
	rule := All(
		Include(func(value constraint) (int, bool) { return value.group, true }),
		CompareBy(
			func(value constraint) (int, bool) { return value.value, true },
			func(value constraint) (Operator, bool) { return value.operator, true },
			cmp.Compare[int],
		),
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

// BenchmarkAllEqualityCandidateFiltering measures a broad equality candidate
// followed by another equality result composed from a wildcard and a concrete
// posting. Apple M1 Max / Go 1.26.0 medians across five 500 ms runs: 9.48 us,
// 32,917 B, 8 allocs when materializing the equality union; an AndAny candidate
// filter measured 9.09 us, 32,961 B, 9 allocs and was rejected. Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllEqualityCandidateFiltering$' -benchmem -benchtime=500ms -count=5 .
func BenchmarkAllEqualityCandidateFiltering(b *testing.B) {
	type constraint struct {
		first  int
		second int
		any    bool
	}
	const entries = 100_000
	constraints := make([]constraint, entries)
	ids := make([]uint32, entries)
	for id := range uint32(entries) {
		constraints[id] = constraint{
			first: int(id % 2), second: int(id % 4), any: id%5 == 0,
		}
		ids[id] = id
	}
	rule := All(
		Include(func(value constraint) (int, bool) { return value.first, true }),
		Include(func(value constraint) (int, bool) { return value.second, !value.any }),
	)
	index, err := New[constraint, uint32](rule).Build(Zip(constraints, ids))
	requireNoBenchmarkError(b, err)
	query := constraint{first: 0, second: 0}
	dst := roaring.New()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dst.Clear()
		index.root.search(query, dst, index.pool)
		plannerBenchmarkCardinality = dst.GetCardinality()
	}
}

// BenchmarkAllOrderedStreaming measures a selective ordered source followed by
// a broader exact equality posting. Apple M1 Max / Go 1.26.0 medians across
// five 1 s runs: 232.8 us, 128,298 B, 20 allocs when intersecting each streamed
// posting before union; 229.8 us, 128,042 B, 19 allocs when materializing the
// ordered union first. Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllOrderedStreaming$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllOrderedStreaming(b *testing.B) {
	type constraint struct {
		group int
		value int
	}
	const entries = 100_000
	constraints := make([]constraint, entries)
	ids := make([]uint32, entries)
	for id := range uint32(entries) {
		constraints[id] = constraint{group: int(id % 2), value: int(id)}
		ids[id] = id
	}
	rule := All(
		GreaterOrEqual(func(value constraint) (int, bool) { return value.value, true }, cmp.Compare[int]),
		Include(func(value constraint) (int, bool) { return value.group, true }),
	)
	index, err := New[constraint, uint32](rule).Build(Zip(constraints, ids))
	requireNoBenchmarkError(b, err)
	query := constraint{group: 0, value: 90_000}
	dst := roaring.New()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dst.Clear()
		index.root.search(query, dst, index.pool)
		plannerBenchmarkCardinality = dst.GetCardinality()
	}
}

// BenchmarkOrderedCandidateFilteringShape compares candidate filtering when a
// wide range finds every candidate near the beginning of its posting walk or
// finds none. Apple M1 Max / Go 1.26.0 medians: current AndAny 168.1 us,
// 126,562 B, 16 allocs for AllEarly and 558.5 ns, 960 B, 3 allocs for None;
// probing the first 16 aggregates before the full AndAny regressed these to
// 176.5 us, 131,349 B, 22 allocs and 819.8 ns, 1,600 B, 5 allocs. Reproduce
// with:
//
// \tgo test -run '^$' -bench '^BenchmarkOrderedCandidateFilteringShape$' -benchmem -benchtime=1s -count=5 .
func BenchmarkOrderedCandidateFilteringShape(b *testing.B) {
	type constraint struct {
		group int
		value int
	}
	const entries = 100_000
	const candidates = 1_024
	constraints := make([]constraint, entries)
	ids := make([]uint32, entries)
	for id := range uint32(entries) {
		constraints[id] = constraint{group: int(id) / candidates, value: int(id)}
		ids[id] = id
	}
	rule := All(
		Include(func(value constraint) (int, bool) { return value.group, true }),
		GreaterOrEqual(func(value constraint) (int, bool) { return value.value, true }, cmp.Compare[int]),
	)
	index, err := New[constraint, uint32](rule).Build(Zip(constraints, ids))
	requireNoBenchmarkError(b, err)
	for _, test := range []struct {
		name  string
		group int
		value int
	}{
		{name: "AllEarly", group: 0, value: entries - 1},
		{name: "None", group: entries/candidates - 1, value: candidates},
	} {
		b.Run(test.name, func(b *testing.B) {
			query := constraint{group: test.group, value: test.value}
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

func requireNoBenchmarkError(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}
