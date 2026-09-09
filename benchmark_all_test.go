//nolint:lll // Migration benchmarks keep legacy pointer getters inline.
package ruleix_test

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/typomaker/ruleix"
)

func benchmarkAllIndex(b *testing.B, nested bool) *ruleix.Index[benchmarkAllValue, int] {
	b.Helper()
	eqA := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.a }))
	eqB := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.b }))
	eqC := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.c }))
	eqD := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.d }))
	var schema ruleix.Rule[benchmarkAllValue]
	if nested {
		schema = ruleix.All(eqA, ruleix.All(eqB, ruleix.All(eqC, eqD)))
	} else {
		schema = ruleix.All(eqA, eqB, eqC, eqD)
	}
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkAllValue, int) {
		return benchmarkAllValue{
			benchmarkPtr(n % 2),
			benchmarkPtr(n % 5),
			benchmarkPtr(n % 10),
			benchmarkPtr(n % benchmarkCardinality),
		}, n
	})
}

func BenchmarkAll(b *testing.B) {
	for _, nested := range []bool{false, true} {
		name := "Flat"
		if nested {
			name = "Nested"
		}
		b.Run(name, func(b *testing.B) {
			ix := benchmarkAllIndex(b, nested)
			query := benchmarkAllValue{benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkLocalAllCardinalityReuse(b *testing.B) {
	ix := benchmarkAllIndex(b, false)
	local := ix.Local()
	query := benchmarkAllValue{benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1)}
	local.Search(query, &benchmarkIntResult)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkIntResult = benchmarkIntResult[:0]
		local.Search(query, &benchmarkIntResult)
	}
}

// BenchmarkAllCardinalityOrder keeps the expensive ordered filter first in the
// schema on purpose. It measures the value of query-dependent candidate sizing:
// replacing it with fixed schema order makes both cases materialize the broad
// ordered result before discovering the empty or narrow equality result.
func BenchmarkAllCardinalityOrder(b *testing.B) {
	schema := ruleix.All(
		ruleix.Greater(ruleix.GetterFromPointer(func(v benchmarkCardinalityOrderValue) *int { return v.threshold }), cmp.Compare[int]),
		ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkCardinalityOrderValue) *int { return v.group })),
	)
	ix := buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkCardinalityOrderValue, int) {
		return benchmarkCardinalityOrderValue{threshold: benchmarkPtr(n), group: benchmarkPtr(n % 100)}, n
	})

	for _, benchmark := range []struct {
		name  string
		group int
	}{
		{name: "ExpensiveBeforeEmpty", group: benchmarkEntries + 1},
		{name: "ExpensiveBeforeNarrow", group: 1},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			query := benchmarkCardinalityOrderValue{
				threshold: benchmarkPtr(benchmarkEntries - 1),
				group:     benchmarkPtr(benchmark.group),
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

// BenchmarkAllSkewedEquality prevents the number of unique map keys from being
// mistaken for query selectivity. The many-values filter is usually selective,
// while the two-value filter is either almost universal or uniquely selective
// depending on the queried value.
func BenchmarkAllSkewedEquality(b *testing.B) {
	schema := ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkSkewedEqualityValue) *int { return v.manyValues })),
		ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkSkewedEqualityValue) *int { return v.skewed })),
	)
	ix := buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkSkewedEqualityValue, int) {
		skewed := 0
		if n == benchmarkEntries-1 {
			skewed = 1
		}
		return benchmarkSkewedEqualityValue{
			manyValues: benchmarkPtr(n % benchmarkCardinality),
			skewed:     benchmarkPtr(skewed),
		}, n
	})

	for _, benchmark := range []struct {
		name   string
		skewed int
	}{
		{name: "ManyKeysSelective", skewed: 0},
		{name: "FewKeysSelective", skewed: 1},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			query := benchmarkSkewedEqualityValue{
				manyValues: benchmarkPtr(benchmarkEntries % benchmarkCardinality),
				skewed:     benchmarkPtr(benchmark.skewed),
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkAllWithExclusions(b *testing.B) {
	for _, candidates := range []int{10, 100} {
		b.Run(fmt.Sprintf("Candidates%d", candidates), func(b *testing.B) {
			schema := ruleix.All(
				ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkExcludeValue) *int { return v.include })),
				ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkExcludeValue) *int { return v.excludeA })),
				ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkExcludeValue) *int { return v.excludeB })),
			)
			index := buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkExcludeValue, int) {
				return benchmarkExcludeValue{
					include:  benchmarkPtr(n % (benchmarkEntries / candidates)),
					excludeA: benchmarkPtr(n % 10),
					excludeB: benchmarkPtr(n % 20),
				}, n
			})
			query := benchmarkExcludeValue{include: benchmarkPtr(1), excludeA: benchmarkPtr(2), excludeB: benchmarkPtr(3)}
			for _, local := range []bool{false, true} {
				name := "Index"
				if local {
					name = "Local"
				}
				b.Run(name, func(b *testing.B) {
					searcher := index.Local()
					var result []int
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						result = result[:0]
						if local {
							searcher.Search(query, &result)
						} else {
							index.Search(query, &result)
						}
					}
				})
			}
		})
	}
}

func BenchmarkParallelSearch(b *testing.B) {
	ix := benchmarkAllIndex(b, true)
	query := benchmarkAllValue{benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1)}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var dst []int
		for pb.Next() {
			dst = dst[:0]
			ix.Search(query, &dst)
			if len(dst) == 0 {
				b.Error("unexpected empty result")
			}
		}
	})
}

func BenchmarkBuildIndex(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ix := buildGenerated(b, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })), benchmarkEntries,
			func(n int) (benchmarkEquality, string) {
				return benchmarkEquality{optional: benchmarkPtr(n)}, fmt.Sprintf("modifier-%d", n)
			})
		benchmarkStringResult = benchmarkStringResult[:0]
		ix.Search(benchmarkEquality{optional: benchmarkPtr(benchmarkEntries / 2)}, &benchmarkStringResult)
	}
	b.ReportMetric(benchmarkEntries, "rules/op")
}

func BenchmarkBuildEqualityCardinality(b *testing.B) {
	for _, cardinality := range []int{1, 2, 4, 8, 16, 32, 100} {
		b.Run(fmt.Sprintf("IDsPerValue/%d", cardinality), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ix := buildGenerated(b, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })), benchmarkEntries,
					func(n int) (benchmarkEquality, int) {
						return benchmarkEquality{optional: benchmarkPtr(n / cardinality)}, n
					})
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(benchmarkEquality{optional: benchmarkPtr(0)}, &benchmarkIntResult)
			}
			b.ReportMetric(float64(benchmarkEntries/cardinality), "values/op")
		})
	}
}

func BenchmarkBuildOrderedIndex(b *testing.B) {
	values := make([]int, benchmarkEntries)
	for n := range values {
		values[n] = n
	}
	queryValue := benchmarkEntries / 2
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ix := buildGenerated(b, ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int]), benchmarkEntries, func(n int) (benchmarkRange, int) { return benchmarkRange{value: &values[n]}, n })
		benchmarkIntResult = benchmarkIntResult[:0]
		ix.Search(benchmarkRange{value: &queryValue}, &benchmarkIntResult)
	}
	b.ReportMetric(benchmarkEntries, "rules/op")
}
