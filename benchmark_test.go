//nolint:lll // Migration benchmarks keep legacy pointer getters inline.
package ruleix_test

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/typomaker/ruleix"
)

const (
	benchmarkEntries     = 10_000
	benchmarkCardinality = 100
)

var (
	benchmarkStringResult []string
	benchmarkIntResult    []int
	benchmarkLocalResult  *ruleix.Local[benchmarkEquality, int]
	benchmarkUint64Result uint64
)

func BenchmarkBitmapResultIteration(b *testing.B) {
	bits := roaring.New()
	bits.AddRange(0, 100_000)
	b.Run("Iterator", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			var sum uint64
			iterator := bits.Iterator()
			for iterator.HasNext() {
				sum += uint64(iterator.Next())
			}
			benchmarkUint64Result = sum
		}
	})
	b.Run("ManyIterator", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			var sum uint64
			iterator := bits.ManyIterator()
			var values [256]uint32
			for count := iterator.NextMany(values[:]); count != 0; count = iterator.NextMany(values[:]) {
				for _, value := range values[:count] {
					sum += uint64(value)
				}
			}
			benchmarkUint64Result = sum
		}
	})
}

func BenchmarkBitmapAndAny(b *testing.B) {
	const postingsCount = 64
	candidates := roaring.New()
	candidates.AddRange(0, 100_000)
	postings := make([]*roaring.Bitmap, postingsCount)
	for posting := range postings {
		bits := roaring.New()
		for id := uint32(posting); id < 100_000; id += postingsCount * 2 {
			bits.Add(id)
		}
		postings[posting] = bits
	}
	b.Run("UnionThenAnd", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			union := roaring.New()
			for _, posting := range postings {
				union.Or(posting)
			}
			result := candidates.Clone()
			result.And(union)
			benchmarkUint64Result = result.GetCardinality()
		}
	})
	b.Run("AndAny", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := candidates.Clone()
			result.AndAny(postings...)
			benchmarkUint64Result = result.GetCardinality()
		}
	})
}

func BenchmarkBitmapFastOr(b *testing.B) {
	for _, postingsCount := range []int{4, 16, 64, 256} {
		postings := make([]*roaring.Bitmap, postingsCount)
		for posting := range postings {
			bits := roaring.New()
			for id := uint32(posting); id < 100_000; id += uint32(postingsCount) {
				bits.Add(id)
			}
			postings[posting] = bits
		}
		b.Run(fmt.Sprintf("Postings%d/Sequential", postingsCount), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := roaring.New()
				for _, posting := range postings {
					result.Or(posting)
				}
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		b.Run(fmt.Sprintf("Postings%d/FastOr", postingsCount), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := roaring.FastOr(postings...)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
	}
}

func BenchmarkBitmapOrCardinality(b *testing.B) {
	for _, overlapPercent := range []int{0, 50, 100} {
		left := roaring.New()
		left.AddRange(0, 100_000)
		right := roaring.New()
		overlap := uint64(overlapPercent * 1_000)
		right.AddRange(100_000-overlap, 200_000-overlap)

		b.Run(fmt.Sprintf("Overlap%d/MaterializeUnion", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := left.Clone()
				result.Or(right)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		b.Run(fmt.Sprintf("Overlap%d/OrCardinality", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = left.OrCardinality(right)
			}
		})
		b.Run(fmt.Sprintf("Overlap%d/OrCardinalityThenUnion", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = left.OrCardinality(right)
				result := left.Clone()
				result.Or(right)
				benchmarkUint64Result += result.GetCardinality()
			}
		})
	}
}

type benchmarkEquality struct {
	optional *int
	required int
}
type benchmarkRange struct {
	operator *ruleix.Operator
	value    *int
}
type benchmarkInterval struct{ from, until *int }
type benchmarkAllValue struct{ a, b, c, d *int }
type benchmarkExcludeValue struct{ include, excludeA, excludeB *int }
type benchmarkCardinalityOrderValue struct{ threshold, group *int }

type benchmarkSkewedEqualityValue struct{ manyValues, skewed *int }

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
		ix := buildGenerated(b, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })), benchmarkEntries,
			func(n int) (benchmarkEquality, int) { return benchmarkEquality{}, n })
		query := benchmarkEquality{optional: benchmarkPtr(42)}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
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
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

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
		ix.Search(benchmarkRange{value: &queryValue}, &benchmarkIntResult)
	}
	b.ReportMetric(benchmarkEntries, "rules/op")
}
