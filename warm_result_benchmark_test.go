package ruleix_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/typomaker/ruleix"
)

type warmResultConstraint struct {
	group  int
	active bool
}

// BenchmarkWarmLocalResultCardinality covers the former 512-ID cutoff and
// wider results. Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 300ms x3: removing
// the cutoff changed 513/1024/2048/4095 IDs from 1,355/2,643/5,121/10,028
// ns/op to 304.5/565.7/1,121/2,189 ns/op, all at 0 B/op and 0 allocs/op.
func BenchmarkWarmLocalResultCardinality(b *testing.B) {
	const largeCardinality = (4 << 10) - 1
	cardinalities := []int{
		1, 8, 45, 64, 65, 96, 97, 128, 129, 256, 257,
		511, 512, 513, 1024, 2048, largeCardinality,
	}
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

// BenchmarkWarmLocalWideResult exercises a result whose alternating internal
// IDs exceed the former 64 KiB result-cache budget. Run with GOMAXPROCS=1,
// -benchtime=200ms, and -count=3 for the comparable numbers documented in
// docs/performance-history.md.
func BenchmarkWarmLocalWideResult(b *testing.B) {
	const entries = 500_000
	constraints := make([]warmResultConstraint, entries)
	ids := make([]int, entries)
	for id := range entries {
		constraints[id] = warmResultConstraint{group: id & 1, active: true}
		ids[id] = id
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
	query := warmResultConstraint{group: 0, active: true}
	matches := make([]int, 0, entries/2)
	for range 4 {
		matches = matches[:0]
		local.Search(query, &matches)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		matches = matches[:0]
		local.Search(query, &matches)
	}
	benchmarkIntResult = matches
}

// BenchmarkWarmLocalWideResultRetainedMemory measures the memory deliberately
// traded for the wide-result speedup. Apple M1 Max, Go 1.26.0, GOMAXPROCS=1,
// 5x x3: the aggressive cache retained 1,076,008 B/Local versus 2,344 B/Local
// with the former 64 KiB result budget.
func BenchmarkWarmLocalWideResultRetainedMemory(b *testing.B) {
	const entries = 500_000
	constraints := make([]warmResultConstraint, entries)
	ids := make([]int, entries)
	for id := range entries {
		constraints[id] = warmResultConstraint{group: id & 1, active: true}
		ids[id] = id
	}
	index, err := ruleix.New[warmResultConstraint, int](ruleix.All(
		ruleix.Include(func(value warmResultConstraint) (int, bool) { return value.group, true }),
		ruleix.Include(func(value warmResultConstraint) (bool, bool) { return value.active, true }),
	)).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	locals := make([]*ruleix.Local[warmResultConstraint, int], b.N)
	matches := make([]int, 0, entries/2)
	query := warmResultConstraint{group: 0, active: true}
	runtime.GC()
	runtime.GC()
	before := heapAlloc()

	b.ResetTimer()
	for i := range b.N {
		local := index.Local()
		for range 4 {
			matches = matches[:0]
			local.Search(query, &matches)
		}
		locals[i] = local
	}
	b.StopTimer()

	runtime.GC()
	runtime.GC()
	after := heapAlloc()
	runtime.KeepAlive(index)
	runtime.KeepAlive(locals)
	runtime.KeepAlive(matches)
	var retained uint64
	if after > before {
		retained = after - before
	}
	b.ReportMetric(float64(retained)/float64(b.N), "retained-B/local")
	for _, local := range locals {
		local.Close()
	}
}

// BenchmarkWarmLocalResultChurn covers working sets immediately below and
// above the four-slot result-cache capacity. The result sizes straddle L4's
// compact limits, making replacement cost and allocations visible beside hit
// latency. L5's latest local medians (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1,
// 1s x 3) are 316.2 ns/op for three keys and 1,408 ns/op for five keys.
func BenchmarkWarmLocalResultChurn(b *testing.B) {
	for _, cardinalities := range []struct {
		name   string
		values []int
	}{
		{name: "WorkingSet3", values: []int{65, 129, 257}},
		{name: "WorkingSet5", values: []int{65, 96, 128, 129, 257}},
	} {
		b.Run(cardinalities.name, func(b *testing.B) {
			benchmarkWarmLocalResultChurn(b, cardinalities.values)
		})
	}
}

func benchmarkWarmLocalResultChurn(b *testing.B, cardinalities []int) {
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
