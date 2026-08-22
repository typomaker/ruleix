package ruleix

import (
	"cmp"
	"fmt"
	"testing"
)

const lossyAllBenchmarkEntries = 10_000

type lossyAllBenchmarkConstraint struct {
	values     [8]string
	thresholds [8]int
	present    [8]bool
}

func lossyAllOrderedBenchmarkSchema(children int) Rule[lossyAllBenchmarkConstraint] {
	rules := make([]Rule[lossyAllBenchmarkConstraint], children)
	for field := range rules {
		field := field
		rules[field] = GreaterOrEqual(func(value lossyAllBenchmarkConstraint) (int, bool) {
			return value.thresholds[field], value.present[field]
		}, cmp.Compare[int])
	}
	return All(rules...)
}

var lossyAllBenchmarkIndex *Index[lossyAllBenchmarkConstraint, int]

func lossyAllBenchmarkData(entries int) ([]lossyAllBenchmarkConstraint, []int) {
	constraints := make([]lossyAllBenchmarkConstraint, entries)
	ids := make([]int, entries)
	for i := range constraints {
		ids[i] = i
		for field := range constraints[i].values {
			// Coprime multipliers keep each leaf's distribution distinct while
			// retaining enough overlap to exercise composite false positives.
			constraints[i].values[field] = fmt.Sprintf("f%d-v%d", field, (i*(field*2+3)+field)%entries)
			constraints[i].thresholds[field] = (i*(field*2+3) + field) % entries
			constraints[i].present[field] = true
		}
	}
	return constraints, ids
}

func lossyAllBenchmarkSchema(children int) Rule[lossyAllBenchmarkConstraint] {
	rules := make([]Rule[lossyAllBenchmarkConstraint], children)
	for field := range rules {
		field := field
		rules[field] = Include(func(value lossyAllBenchmarkConstraint) (string, bool) {
			return value.values[field], value.present[field]
		})
	}
	return All(rules...)
}

func BenchmarkLossyAllPlanning(b *testing.B) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	for _, children := range []int{2, 4, 8} {
		exactBytes := lossyAllBenchmarkExactBytes(b, constraints, ids, children)
		for _, tc := range []struct {
			name    string
			percent uint64
		}{
			{name: "Exact", percent: 100},
			{name: "Budget50", percent: 50},
			{name: "Budget25", percent: 25},
		} {
			b.Run(fmt.Sprintf("Children%d/%s", children, tc.name), func(b *testing.B) {
				limit := exactBytes * tc.percent / 100
				schema := lossyAllBenchmarkSchema(children)
				if tc.percent != 100 {
					schema = Lossy(schema, MemoryLimit(limit))
				}
				builder := New[lossyAllBenchmarkConstraint, int](schema)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					index, err := builder.Build(Zip(constraints, ids))
					if err != nil {
						b.Fatal(err)
					}
					lossyAllBenchmarkIndex = index
				}
				b.ReportMetric(float64(limit), "limit-bytes")
			})
		}
	}
}

func BenchmarkLossyAllOrderedPlanning(b *testing.B) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	for _, children := range []int{2, 4} {
		exactBytes := lossyAllBenchmarkExactBytesForSchema(b, constraints, ids, lossyAllOrderedBenchmarkSchema(children))
		b.Run(fmt.Sprintf("Children%d/Budget75", children), func(b *testing.B) {
			limit := exactBytes * 3 / 4
			builder := New[lossyAllBenchmarkConstraint, int](Lossy(lossyAllOrderedBenchmarkSchema(children), MemoryLimit(limit)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				index, err := builder.Build(Zip(constraints, ids))
				if err != nil {
					b.Fatal(err)
				}
				lossyAllBenchmarkIndex = index
			}
			b.ReportMetric(float64(limit), "limit-bytes")
		})
	}
}

func BenchmarkLossyAllSearchQuality(b *testing.B) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	const children = 4
	exactBytes := lossyAllBenchmarkExactBytes(b, constraints, ids, children)
	for _, tc := range []struct {
		name    string
		percent uint64
	}{
		{name: "Exact", percent: 100},
		{name: "Budget50", percent: 50},
		{name: "Budget25", percent: 25},
	} {
		b.Run(tc.name, func(b *testing.B) {
			schema := lossyAllBenchmarkSchema(children)
			if tc.percent != 100 {
				schema = Lossy(schema, MemoryLimit(exactBytes*tc.percent/100))
			}
			index, err := New[lossyAllBenchmarkConstraint, int](schema).Build(Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			queries := constraints[:256]
			var matches []int
			var totalMatches uint64
			for _, query := range queries {
				matches = matches[:0]
				index.Search(query, &matches)
				totalMatches += uint64(len(matches))
			}
			exactMatches := uint64(len(queries))
			falsePositives := totalMatches - exactMatches
			possibleFalsePositives := uint64(len(queries)) * uint64(len(constraints)-1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				matches = matches[:0]
				index.Search(queries[i%len(queries)], &matches)
			}
			b.ReportMetric(float64(totalMatches)/float64(len(queries)), "candidates/query")
			b.ReportMetric(float64(falsePositives)/float64(possibleFalsePositives), "false-positive-rate")
			lossyAllBenchmarkIndex = index
		})
	}
}

func lossyAllBenchmarkExactBytes(
	b testing.TB,
	constraints []lossyAllBenchmarkConstraint,
	ids []int,
	children int,
) uint64 {
	b.Helper()
	return lossyAllBenchmarkExactBytesForSchema(b, constraints, ids, lossyAllBenchmarkSchema(children))
}

func lossyAllBenchmarkExactBytesForSchema(
	b testing.TB,
	constraints []lossyAllBenchmarkConstraint,
	ids []int,
	schema Rule[lossyAllBenchmarkConstraint],
) uint64 {
	b.Helper()
	var inspector Inspector
	_, err := New[lossyAllBenchmarkConstraint, int](Inspect(
		&inspector,
		Lossy(schema, MemoryLimit(^uint64(0))),
	)).Build(Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	usage, ok := inspector.MemoryUsage()
	if !ok {
		b.Fatal("exact memory usage is unavailable")
	}
	return usage
}
