package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/typomaker/ruleix"
)

type warmResultConstraint struct {
	group int
}

// BenchmarkWarmLocalResultCardinality last local baseline (Apple M1 Max, Go 1.26.0):
// go test -run '^$' -bench '^BenchmarkWarmLocalResultCardinality$' -benchmem -benchtime=1s -count=5 .
// Medians: Empty 27.19 ns/op, Singleton 39.60 ns/op, Small8 58.47 ns/op,
// Large4095 9,925 ns/op; every case measured 0 B/op and 0 allocs/op.
func BenchmarkWarmLocalResultCardinality(b *testing.B) {
	const largeCardinality = (4 << 10) - 1
	constraints := make([]warmResultConstraint, 0, 1+8+largeCardinality)
	ids := make([]int, 0, cap(constraints))
	appendGroup := func(group, count int) {
		for range count {
			constraints = append(constraints, warmResultConstraint{group: group})
			ids = append(ids, len(ids))
		}
	}
	appendGroup(1, 1)
	appendGroup(2, 8)
	appendGroup(3, largeCardinality)
	index, err := ruleix.New[warmResultConstraint, int](
		ruleix.Include(func(value warmResultConstraint) (int, bool) { return value.group, true }),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}

	for _, benchmark := range []struct {
		name  string
		group int
		want  int
	}{
		{name: "Empty", group: 0, want: 0},
		{name: "Singleton", group: 1, want: 1},
		{name: "Small8", group: 2, want: 8},
		{name: fmt.Sprintf("Large%d", largeCardinality), group: 3, want: largeCardinality},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			local := index.Local()
			b.Cleanup(local.Close)
			matches := make([]int, 0, benchmark.want)
			query := warmResultConstraint{group: benchmark.group}
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
