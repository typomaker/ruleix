package ruleix_test

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/albertsultanov/ruleix"
)

const (
	benchmarkEntries     = 10_000
	benchmarkCardinality = 100
)

var (
	benchmarkStringResult []string
	benchmarkIntResult    []int
)

type benchmarkEquality struct {
	optional *int
	required int
}
type benchmarkRange struct {
	operator string
	value    *int
}
type benchmarkInterval struct{ from, until *int }
type benchmarkAllValue struct{ a, b, c, d *int }

func benchmarkPtr(value int) *int { return &value }

func buildGenerated[C any, ID comparable](
	b *testing.B,
	schema ruleix.Rule[C],
	count int,
	entry func(int) (C, ID),
) *ruleix.Index[C, ID] {
	b.Helper()
	ix, err := ruleix.New[C, ID](schema).Build(func(yield func(C, ID) bool) {
		for n := 0; n < count; n++ {
			value, id := entry(n)
			if !yield(value, id) {
				return
			}
		}
	})
	if err != nil {
		b.Fatal(err)
	}
	return ix
}

func benchmarkEqIndex(b *testing.B, highCardinality bool) *ruleix.Index[benchmarkEquality, int] {
	b.Helper()
	schema := ruleix.Include(func(v benchmarkEquality) *int { return v.optional })
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkEquality, int) {
		value := n
		if highCardinality {
			value %= benchmarkCardinality
		}
		return benchmarkEquality{optional: benchmarkPtr(value)}, n
	})
}

func BenchmarkEq(b *testing.B) {
	for _, high := range []bool{false, true} {
		cardinality := "LowCardinality"
		if high {
			cardinality = "HighCardinality"
		}
		b.Run(cardinality+"/Hit", func(b *testing.B) {
			ix := benchmarkEqIndex(b, high)
			query := benchmarkEquality{optional: benchmarkPtr(42)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ix.Search(query, &benchmarkIntResult)
			}
		})
		b.Run(cardinality+"/Miss", func(b *testing.B) {
			ix := benchmarkEqIndex(b, high)
			query := benchmarkEquality{optional: benchmarkPtr(benchmarkEntries + 1)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
	b.Run("Wildcard", func(b *testing.B) {
		ix := buildGenerated(b, ruleix.Include(func(v benchmarkEquality) *int { return v.optional }), benchmarkEntries,
			func(n int) (benchmarkEquality, int) { return benchmarkEquality{}, n })
		query := benchmarkEquality{optional: benchmarkPtr(42)}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ix.Search(query, &benchmarkIntResult)
		}
	})
}

func benchmarkOrderedIndex(b *testing.B, kind string) *ruleix.Index[benchmarkRange, int] {
	b.Helper()
	var schema ruleix.Rule[benchmarkRange]
	switch kind {
	case "GTE":
		schema = ruleix.GreaterOrEqual(func(v benchmarkRange) *int { return v.value }, cmp.Compare[int])
	case "LTE":
		schema = ruleix.LessOrEqual(func(v benchmarkRange) *int { return v.value }, cmp.Compare[int])
	case "CompareBy":
		schema = ruleix.CompareBy(
			func(v benchmarkRange) string { return v.operator },
			func(v benchmarkRange) *int { return v.value },
			cmp.Compare[int],
		)
	default:
		b.Fatalf("unknown ordered benchmark %q", kind)
	}
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkRange, int) {
		operator := ">="
		if n%2 == 1 {
			operator = "<="
		}
		return benchmarkRange{operator: operator, value: benchmarkPtr(n)}, n
	})
}

func BenchmarkOrdered(b *testing.B) {
	positions := []struct {
		name  string
		value int
	}{{"Start", 0}, {"Middle", benchmarkEntries / 2}, {"End", benchmarkEntries - 1}}
	for _, kind := range []string{"GTE", "LTE", "CompareBy"} {
		for _, position := range positions {
			b.Run(kind+"/"+position.name, func(b *testing.B) {
				ix := benchmarkOrderedIndex(b, kind)
				query := benchmarkRange{value: benchmarkPtr(position.value)}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					ix.Search(query, &benchmarkIntResult)
				}
			})
		}
	}
}

func BenchmarkBetweenManyUniqueBounds(b *testing.B) {
	ix := buildGenerated(b, ruleix.Between(
		func(v benchmarkInterval) *int { return v.from },
		func(v benchmarkInterval) *int { return v.until },
		cmp.Compare[int],
	), benchmarkEntries, func(n int) (benchmarkInterval, int) {
		return benchmarkInterval{from: benchmarkPtr(n), until: benchmarkPtr(benchmarkEntries*2 - n)}, n
	})
	query := benchmarkInterval{from: benchmarkPtr(benchmarkEntries / 2), until: benchmarkPtr(benchmarkEntries + benchmarkEntries/2)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.Search(query, &benchmarkIntResult)
	}
}

func BenchmarkBetweenNarrowIntersection(b *testing.B) {
	from := func(v benchmarkInterval) *int { return v.from }
	until := func(v benchmarkInterval) *int { return v.until }
	for _, tt := range []struct {
		name   string
		schema ruleix.Rule[benchmarkInterval]
	}{
		{"Specialized", ruleix.Between(from, until, cmp.Compare[int])},
		{"Composed", ruleix.All(
			ruleix.GreaterOrEqual(from, cmp.Compare[int]),
			ruleix.LessOrEqual(until, cmp.Compare[int]),
		)},
	} {
		b.Run(tt.name, func(b *testing.B) {
			ix := buildGenerated(b, tt.schema, benchmarkEntries, func(n int) (benchmarkInterval, int) {
				switch {
				case n == 0:
					return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(10_000)}, n
				case n < benchmarkEntries/2:
					return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(6_000)}, n
				default:
					return benchmarkInterval{from: benchmarkPtr(6_000), until: benchmarkPtr(10_000)}, n
				}
			})
			query := benchmarkInterval{from: benchmarkPtr(5_000), until: benchmarkPtr(7_000)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkBetweenSelectiveSide(b *testing.B) {
	from := func(v benchmarkInterval) *int { return v.from }
	until := func(v benchmarkInterval) *int { return v.until }
	for _, tt := range []struct {
		name   string
		schema ruleix.Rule[benchmarkInterval]
	}{
		{"Specialized", ruleix.Between(from, until, cmp.Compare[int])},
		{"Composed", ruleix.All(
			ruleix.GreaterOrEqual(from, cmp.Compare[int]),
			ruleix.LessOrEqual(until, cmp.Compare[int]),
		)},
	} {
		b.Run(tt.name, func(b *testing.B) {
			ix := buildGenerated(b, tt.schema, benchmarkEntries, func(n int) (benchmarkInterval, int) {
				return benchmarkInterval{from: benchmarkPtr(n), until: benchmarkPtr(benchmarkEntries + n)}, n
			})
			query := benchmarkInterval{from: benchmarkPtr(31), until: benchmarkPtr(benchmarkEntries)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkFilterWildcard(b *testing.B) {
	tests := []struct {
		name   string
		schema ruleix.Rule[benchmarkRange]
	}{
		{"GTE", ruleix.GreaterOrEqual(func(v benchmarkRange) *int { return v.value }, cmp.Compare[int])},
		{"LTE", ruleix.LessOrEqual(func(v benchmarkRange) *int { return v.value }, cmp.Compare[int])},
		{"CompareBy", ruleix.CompareBy(
			func(v benchmarkRange) string { return v.operator },
			func(v benchmarkRange) *int { return v.value },
			cmp.Compare[int],
		)},
		{"Not", ruleix.Exclude(func(v benchmarkRange) *int { return v.value })},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ix := buildGenerated(b, tt.schema, benchmarkEntries,
				func(n int) (benchmarkRange, int) { return benchmarkRange{}, n })
			query := benchmarkRange{value: benchmarkPtr(42)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func benchmarkAllIndex(b *testing.B, nested bool) *ruleix.Index[benchmarkAllValue, int] {
	b.Helper()
	eqA := ruleix.Include(func(v benchmarkAllValue) *int { return v.a })
	eqB := ruleix.Include(func(v benchmarkAllValue) *int { return v.b })
	eqC := ruleix.Include(func(v benchmarkAllValue) *int { return v.c })
	eqD := ruleix.Include(func(v benchmarkAllValue) *int { return v.d })
	var schema ruleix.Rule[benchmarkAllValue]
	if nested {
		schema = ruleix.All(eqA, ruleix.All(eqB, ruleix.All(eqC, eqD)))
	} else {
		schema = ruleix.All(eqA, eqB, eqC, eqD)
	}
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkAllValue, int) {
		return benchmarkAllValue{benchmarkPtr(n % 2), benchmarkPtr(n % 5), benchmarkPtr(n % 10), benchmarkPtr(n % benchmarkCardinality)}, n
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
				ix.Search(query, &benchmarkIntResult)
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
		ix := buildGenerated(b, ruleix.Include(func(v benchmarkEquality) *int { return v.optional }), benchmarkEntries,
			func(n int) (benchmarkEquality, string) {
				return benchmarkEquality{optional: benchmarkPtr(n)}, fmt.Sprintf("modifier-%d", n)
			})
		ix.Search(benchmarkEquality{optional: benchmarkPtr(benchmarkEntries / 2)}, &benchmarkStringResult)
	}
	b.ReportMetric(benchmarkEntries, "rules/op")
}

func BenchmarkBuildEqualityCardinality(b *testing.B) {
	for _, cardinality := range []int{1, 2, 4, 8, 16, 32, 100} {
		b.Run(fmt.Sprintf("IDsPerValue/%d", cardinality), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ix := buildGenerated(b, ruleix.Include(func(v benchmarkEquality) *int { return v.optional }), benchmarkEntries,
					func(n int) (benchmarkEquality, int) {
						return benchmarkEquality{optional: benchmarkPtr(n / cardinality)}, n
					})
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
		ix := buildGenerated(b, ruleix.GreaterOrEqual(
			func(v benchmarkRange) *int { return v.value },
			cmp.Compare[int],
		), benchmarkEntries, func(n int) (benchmarkRange, int) { return benchmarkRange{value: &values[n]}, n })
		ix.Search(benchmarkRange{value: &queryValue}, &benchmarkIntResult)
	}
	b.ReportMetric(benchmarkEntries, "rules/op")
}
