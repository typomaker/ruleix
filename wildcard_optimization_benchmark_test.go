package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/typomaker/ruleix"
)

const wildcardBenchmarkMaxFilters = 64

type wildcardBenchmarkConstraint struct {
	values [wildcardBenchmarkMaxFilters]*int
}

var wildcardBenchmarkIndexResult *ruleix.Index[wildcardBenchmarkConstraint, int]

type wildcardBenchmarkCase struct {
	name          string
	filters       int
	wildcardCount int
}

func wildcardBenchmarkCases() []wildcardBenchmarkCase {
	var cases []wildcardBenchmarkCase
	for _, filters := range []int{16, 64} {
		for _, wildcardPercent := range []int{0, 75, 100} {
			cases = append(cases, wildcardBenchmarkCase{
				name:          fmt.Sprintf("Filters%d/Wildcard%dPercent", filters, wildcardPercent),
				filters:       filters,
				wildcardCount: filters * wildcardPercent / 100,
			})
		}
	}
	return cases
}

func wildcardBenchmarkSchema(filters int) ruleix.Rule[wildcardBenchmarkConstraint] {
	rules := make([]ruleix.Rule[wildcardBenchmarkConstraint], filters)
	for i := range rules {
		position := i
		rules[i] = ruleix.Include(ruleix.GetterFromPointer(func(value wildcardBenchmarkConstraint) *int {
			return value.values[position]
		}))
	}
	return ruleix.All(rules...)
}

func wildcardBenchmarkData(benchmark wildcardBenchmarkCase) ([]wildcardBenchmarkConstraint, []int) {
	values := make([]int, benchmarkCardinality)
	for i := range values {
		values[i] = i
	}
	constraints := make([]wildcardBenchmarkConstraint, benchmarkEntries)
	ids := make([]int, benchmarkEntries)
	for i := range constraints {
		ids[i] = i
		value := &values[i%len(values)]
		for filter := benchmark.wildcardCount; filter < benchmark.filters; filter++ {
			constraints[i].values[filter] = value
		}
	}
	return constraints, ids
}

func wildcardBenchmarkQuery(benchmark wildcardBenchmarkCase) wildcardBenchmarkConstraint {
	query := wildcardBenchmarkConstraint{}
	value := 42
	for filter := benchmark.wildcardCount; filter < benchmark.filters; filter++ {
		query.values[filter] = &value
	}
	return query
}

func BenchmarkWildcardHeavySearch(b *testing.B) {
	for _, benchmark := range wildcardBenchmarkCases() {
		b.Run(benchmark.name, func(b *testing.B) {
			constraints, ids := wildcardBenchmarkData(benchmark)
			index, err := ruleix.New[wildcardBenchmarkConstraint, int](
				wildcardBenchmarkSchema(benchmark.filters),
			).Build(ruleix.Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			query := wildcardBenchmarkQuery(benchmark)
			matches := make([]int, 0, benchmarkEntries)
			index.Search(query, &matches)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				index.Search(query, &matches)
			}
			benchmarkIntResult = matches
		})
	}
}

func BenchmarkWildcardHeavyBuild(b *testing.B) {
	for _, benchmark := range wildcardBenchmarkCases() {
		b.Run(benchmark.name, func(b *testing.B) {
			constraints, ids := wildcardBenchmarkData(benchmark)
			builder := ruleix.New[wildcardBenchmarkConstraint, int](
				wildcardBenchmarkSchema(benchmark.filters),
			)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				index, err := builder.Build(ruleix.Zip(constraints, ids))
				if err != nil {
					b.Fatal(err)
				}
				wildcardBenchmarkIndexResult = index
			}
			b.ReportMetric(benchmarkEntries, "entries/op")
		})
	}
}
