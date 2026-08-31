package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/typomaker/ruleix"
)

type warmResultConstraint struct {
	group  int
	active bool
}

// BenchmarkWarmLocalResultCardinality L4 parent baseline is recorded in
// ROADMAP_HISTORY.md. Keep the boundary cases synchronized with the compact
// result limits evaluated there so later measurements remain comparable.
func BenchmarkWarmLocalResultCardinality(b *testing.B) {
	const largeCardinality = (4 << 10) - 1
	cardinalities := []int{1, 8, 45, 64, 65, 96, 97, 128, 129, 256, 257, largeCardinality}
	total := 0
	for _, cardinality := range cardinalities {
		total += cardinality
	}
	constraints := make([]warmResultConstraint, 0, total)
	ids := make([]int, 0, cap(constraints))
	appendGroup := func(group, count int) {
		for range count {
			constraints = append(constraints, warmResultConstraint{group: group, active: true})
			ids = append(ids, len(ids))
		}
	}
	for group, cardinality := range cardinalities {
		appendGroup(group+1, cardinality)
	}
	index, err := ruleix.New[warmResultConstraint, int](ruleix.All(
		ruleix.Include(func(value warmResultConstraint) (int, bool) { return value.group, true }),
		ruleix.Include(func(value warmResultConstraint) (bool, bool) { return value.active, true }),
	)).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}

	benchmarks := []struct {
		name  string
		group int
		want  int
	}{{name: "Empty", group: 0, want: 0}}
	for group, cardinality := range cardinalities {
		benchmarks = append(benchmarks, struct {
			name  string
			group int
			want  int
		}{name: fmt.Sprintf("Cardinality%d", cardinality), group: group + 1, want: cardinality})
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			local := index.Local()
			b.Cleanup(local.Close)
			matches := make([]int, 0, benchmark.want)
			query := warmResultConstraint{group: benchmark.group, active: true}
			for range 3 {
				matches = matches[:0]
				local.Search(query, &matches)
			}
			if len(matches) != benchmark.want {
				b.Fatalf("got %d matches, want %d", len(matches), benchmark.want)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				matches = matches[:0]
				local.Search(query, &matches)
			}
			benchmarkIntResult = matches
		})
	}
}

// BenchmarkWarmLocalResultChurn alternates three exact keys through the two
// result-cache slots. The result sizes straddle L4's candidate compact limits,
// making cache replacement cost and allocations visible beside hit latency.
func BenchmarkWarmLocalResultChurn(b *testing.B) {
	cardinalities := []int{65, 129, 257}
	total := 0
	for _, cardinality := range cardinalities {
		total += cardinality
	}
	constraints := make([]warmResultConstraint, 0, total)
	ids := make([]int, 0, total)
	queries := make([]warmResultConstraint, len(cardinalities))
	for group, cardinality := range cardinalities {
		queries[group] = warmResultConstraint{group: group + 1, active: true}
		for range cardinality {
			constraints = append(constraints, queries[group])
			ids = append(ids, len(ids))
		}
	}
	index, err := ruleix.New[warmResultConstraint, int](ruleix.All(
		ruleix.Include(func(value warmResultConstraint) (int, bool) { return value.group, true }),
		ruleix.Include(func(value warmResultConstraint) (bool, bool) { return value.active, true }),
	)).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	local := index.Local()
	b.Cleanup(local.Close)
	matches := make([]int, 0, cardinalities[len(cardinalities)-1])
	for i := range 6 {
		matches = matches[:0]
		local.Search(queries[i%len(queries)], &matches)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		matches = matches[:0]
		local.Search(queries[i%len(queries)], &matches)
	}
	benchmarkIntResult = matches
}
