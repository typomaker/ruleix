//nolint:lll // Migration benchmarks keep legacy pointer getters inline.
package ruleix_test

import (
	"cmp"
	"testing"

	"github.com/typomaker/ruleix"
)

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
	schema := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional }))
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
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
		b.Run(cardinality+"/Miss", func(b *testing.B) {
			ix := benchmarkEqIndex(b, high)
			query := benchmarkEquality{optional: benchmarkPtr(benchmarkEntries + 1)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
	b.Run("Wildcard", func(b *testing.B) {
		ix := buildGenerated(b, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })), benchmarkEntries,
			func(n int) (benchmarkEquality, int) { return benchmarkEquality{}, n })
		query := benchmarkEquality{optional: benchmarkPtr(42)}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkIntResult = benchmarkIntResult[:0]
			ix.Search(query, &benchmarkIntResult)
		}
	})
}

func BenchmarkLocalCreation(b *testing.B) {
	ix := benchmarkEqIndex(b, true)
	b.Run("Cold", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkLocalResult = ix.Local()
		}
	})
	b.Run("Primed", func(b *testing.B) {
		query := benchmarkEquality{optional: benchmarkPtr(42)}
		var dst []int
		b.ReportAllocs()
		for range b.N {
			local := ix.Local()
			dst = dst[:0]
			local.Search(query, &dst)
			benchmarkLocalResult = local
		}
	})
}

func BenchmarkLocalEqualityReuse(b *testing.B) {
	type query struct{ store, region *int }
	ix := buildGenerated(b, ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(v query) *int { return v.store })),
		ruleix.Include(ruleix.GetterFromPointer(func(v query) *int { return v.region })),
	), benchmarkEntries, func(n int) (query, int) {
		switch {
		case n < 10:
			return query{store: benchmarkPtr(10), region: benchmarkPtr(20)}, n
		case n < 20:
			return query{store: benchmarkPtr(10), region: benchmarkPtr(30)}, n
		case n < 2_000:
			return query{region: benchmarkPtr(99)}, n
		case n < 4_000:
			return query{store: benchmarkPtr(11)}, n
		case n < 7_000:
			return query{store: benchmarkPtr(10), region: benchmarkPtr(99)}, n
		default:
			return query{store: benchmarkPtr(11), region: benchmarkPtr(20 + 10*(n&1))}, n
		}
	})
	store, regions := 10, [2]int{20, 30}

	for _, local := range []bool{false, true} {
		name := "Index"
		if local {
			name = "Local"
		}
		b.Run(name, func(b *testing.B) {
			searcher := ix.Local()
			var dst []int
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				q := query{store: &store, region: &regions[n&1]}
				dst = dst[:0]
				if local {
					searcher.Search(q, &dst)
				} else {
					ix.Search(q, &dst)
				}
			}
		})
	}
}

func benchmarkOrderedIndex(b *testing.B, kind string) *ruleix.Index[benchmarkRange, int] {
	b.Helper()
	var schema ruleix.Rule[benchmarkRange]
	switch kind {
	case "GTE":
		schema = ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])
	case "LTE":
		schema = ruleix.LessOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])
	case "CompareBy":
		schema = ruleix.CompareBy(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), ruleix.GetterFromPointer(func(v benchmarkRange) *ruleix.Operator { return v.operator }), cmp.Compare[int])
	default:
		b.Fatalf("unknown ordered benchmark %q", kind)
	}
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkRange, int) {
		return benchmarkRange{operator: ptr(ruleix.OperatorGTE), value: benchmarkPtr(n)}, n
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
				query := benchmarkRange{operator: ptr(ruleix.OperatorGTE), value: benchmarkPtr(position.value)}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkIntResult = benchmarkIntResult[:0]
					ix.Search(query, &benchmarkIntResult)
				}
			})
		}
	}
}

func BenchmarkLocalOrderedReuse(b *testing.B) {
	ix := benchmarkOrderedIndex(b, "GTE")
	modes := []struct {
		name  string
		value func(int) int
	}{
		{"Repeat", func(int) int { return benchmarkEntries / 2 }},
		{"Alternate", func(n int) int { return benchmarkEntries/2 + (n & 1) }},
		{"Cycle3", func(n int) int { return benchmarkEntries/2 + n%3 }},
		{"Cycle4", func(n int) int { return benchmarkEntries/2 + n%4 }},
		{"HotWithInterlopers", func(n int) int { return benchmarkEntries/2 + [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) int { return n % benchmarkEntries }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					value := mode.value(n)
					query := benchmarkRange{value: &value}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkLocalCompareByReuse(b *testing.B) {
	ix := benchmarkOrderedIndex(b, "CompareBy")
	modes := []struct {
		name  string
		value func(int) int
	}{
		{"Repeat", func(int) int { return benchmarkEntries / 2 }},
		{"Alternate", func(n int) int { return benchmarkEntries/2 + (n & 1) }},
		{"HotWithInterlopers", func(n int) int { return benchmarkEntries/2 + [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) int { return n % benchmarkEntries }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					value := mode.value(n)
					query := benchmarkRange{value: &value}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkBetweenManyUniqueBounds(b *testing.B) {
	ix := buildGenerated(b, ruleix.Between(ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.from }), ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.until }), cmp.Compare[int]), benchmarkEntries, func(n int) (benchmarkInterval, int) {
		return benchmarkInterval{from: benchmarkPtr(n), until: benchmarkPtr(benchmarkEntries*2 - n)}, n
	})
	query := benchmarkInterval{
		from:  benchmarkPtr(benchmarkEntries / 2),
		until: benchmarkPtr(benchmarkEntries + benchmarkEntries/2),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkIntResult = benchmarkIntResult[:0]
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
		{"Specialized", ruleix.Between(ruleix.GetterFromPointer(from), ruleix.GetterFromPointer(until), cmp.Compare[int])},
		{"Composed", ruleix.All(
			ruleix.GreaterOrEqual(ruleix.GetterFromPointer(from), cmp.Compare[int]),
			ruleix.LessOrEqual(ruleix.GetterFromPointer(until), cmp.Compare[int]),
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
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkLocalBetweenReuse(b *testing.B) {
	ix := buildGenerated(b, ruleix.Between(ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.from }), ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.until }), cmp.Compare[int]), benchmarkEntries, func(n int) (benchmarkInterval, int) {
		switch {
		case n == 0:
			return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(10_000)}, n
		case n < benchmarkEntries/2:
			return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(6_000)}, n
		default:
			return benchmarkInterval{from: benchmarkPtr(6_000), until: benchmarkPtr(10_000)}, n
		}
	})
	modes := []struct {
		name  string
		value func(int) (int, int)
	}{
		{"Repeat", func(int) (int, int) { return 5_000, 7_000 }},
		{"Alternate", func(n int) (int, int) { return 5_000, 7_000 + (n & 1) }},
		{"Cycle3", func(n int) (int, int) { return 5_000, 7_000 + n%3 }},
		{"Cycle4", func(n int) (int, int) { return 5_000, 7_000 + n%4 }},
		{"HotWithInterlopers", func(n int) (int, int) { return 5_000, 7_000 + [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) (int, int) { return n % 5_000, 7_000 }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					from, until := mode.value(n)
					query := benchmarkInterval{from: &from, until: &until}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkLocalExcludeReuse(b *testing.B) {
	const entries = 32
	ix := buildGenerated(b, ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value })), entries, func(n int) (benchmarkRange, int) {
		value := n & 1
		return benchmarkRange{value: benchmarkPtr(value)}, n
	})
	modes := []struct {
		name  string
		value func(int) int
	}{
		{"Repeat", func(int) int { return 0 }},
		{"Alternate", func(n int) int { return n & 1 }},
		{"HotWithInterlopers", func(n int) int { return [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) int { return n }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					value := mode.value(n)
					query := benchmarkRange{value: &value}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkBetweenSelectiveSide(b *testing.B) {
	from := func(v benchmarkInterval) *int { return v.from }
	until := func(v benchmarkInterval) *int { return v.until }
	for _, tt := range []struct {
		name   string
		schema ruleix.Rule[benchmarkInterval]
	}{
		{"Specialized", ruleix.Between(ruleix.GetterFromPointer(from), ruleix.GetterFromPointer(until), cmp.Compare[int])},
		{"Composed", ruleix.All(
			ruleix.GreaterOrEqual(ruleix.GetterFromPointer(from), cmp.Compare[int]),
			ruleix.LessOrEqual(ruleix.GetterFromPointer(until), cmp.Compare[int]),
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
				benchmarkIntResult = benchmarkIntResult[:0]
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
		{"GTE", ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])},
		{"LTE", ruleix.LessOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])},
		{"CompareBy", ruleix.CompareBy(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), ruleix.GetterFromPointer(func(v benchmarkRange) *ruleix.Operator { return v.operator }), cmp.Compare[int])},
		{"Not", ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }))},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ix := buildGenerated(b, tt.schema, benchmarkEntries,
				func(n int) (benchmarkRange, int) { return benchmarkRange{}, n })
			op := ruleix.OperatorGTE
			query := benchmarkRange{operator: &op, value: benchmarkPtr(42)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}
