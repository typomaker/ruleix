package ruleix

import (
	"runtime"
	"testing"
)

type inspectBenchmarkConstraint struct {
	selective int
	broad     int
}

// BenchmarkInspectRuntimeOverhead compares equivalent compiled trees with and
// without explicitly enabled runtime observation. The leaf cases measure the
// observation wrapper directly; the All cases also cover candidate checks
// and the specialized top-level append path.
//
// Apple M1 Max, 2026-08-24, medians of five 300 ms runs after moving runtime
// telemetry off ordinary searches: Index leaf 92.79 ns/op plain and 91.79
// ns/op inspected; Index All 193.4 ns/op plain and 192.7 ns/op inspected; warm
// Local leaf 40.56 ns/op plain and 40.77 ns/op inspected; warm Local All 110.9
// ns/op plain and 111.0 ns/op inspected. Reproduce with: go test -run '^$' -bench
// '^BenchmarkInspectRuntimeOverhead/' -benchmem -benchtime=300ms -count=5 .
//
//nolint:lll // Keeping each benchmark schema on one line makes the matrix easier to compare.
func BenchmarkInspectRuntimeOverhead(b *testing.B) {
	const rules = 10_000
	constraints := make([]inspectBenchmarkConstraint, rules)
	ids := make([]int, rules)
	for i := range rules {
		constraints[i] = inspectBenchmarkConstraint{selective: i, broad: i % 4}
		ids[i] = i
	}
	entries := Zip(constraints, ids)
	query := inspectBenchmarkConstraint{selective: rules / 2, broad: 0}
	selective := func(v inspectBenchmarkConstraint) (int, bool) { return v.selective, true }
	broad := func(v inspectBenchmarkConstraint) (int, bool) { return v.broad, true }

	for _, local := range []bool{false, true} {
		mode := "Index"
		if local {
			mode = "Local"
		}
		b.Run(mode, func(b *testing.B) {
			b.Run("Leaf", func(b *testing.B) {
				benchmarkInspectSearch(b, Include(selective), nil, entries, query, local, false, 1)
			})
			b.Run("InspectedLeaf", func(b *testing.B) {
				var inspector Inspector
				benchmarkInspectSearch(b, Inspect(&inspector, Include(selective)), inspector, entries, query, local, false, 1)
			})
			b.Run("All", func(b *testing.B) {
				benchmarkInspectSearch(b, All(Include(selective), Include(broad)), nil, entries, query, local, false, 1)
			})
			b.Run("InspectedAll", func(b *testing.B) {
				var inspector Inspector
				benchmarkInspectSearch(b, Inspect(&inspector, All(Include(selective), Include(broad))), inspector, entries, query, local, false, 1)
			})
			b.Run("AllInspectedBroadChild", func(b *testing.B) {
				var inspector Inspector
				benchmarkInspectSearch(b, All(Include(selective), Inspect(&inspector, Include(broad))), inspector, entries, query, local, false, 1)
			})
		})
	}
}

// BenchmarkInspectCandidateCheckOverhead isolates an inspected child across
// the maximum four-ID All candidate scan. Apple M1 Max, 2026-08-24, medians of
// seven 500 ms runs: 281.0 ns/op through the plain tree and 303.2 ns/op through
// the explicitly selected observed tree, both 32 B/op and 2 allocs/op.
// Reproduce with: go test -run '^$' -bench
// '^BenchmarkInspectCandidateCheckOverhead'
// -benchmem -benchtime=500ms -count=7 .
func BenchmarkInspectCandidateCheckOverhead(b *testing.B) {
	const rules = 10_000
	constraints := make([]inspectBenchmarkConstraint, rules)
	ids := make([]int, rules)
	for i := range rules {
		constraints[i] = inspectBenchmarkConstraint{selective: i % 2500, broad: 0}
		ids[i] = i
	}
	entries := Zip(constraints, ids)
	query := inspectBenchmarkConstraint{selective: 0, broad: 0}
	selective := func(v inspectBenchmarkConstraint) (int, bool) { return v.selective, true }
	broad := func(v inspectBenchmarkConstraint) (int, bool) { return v.broad, true }

	b.Run("Plain", func(b *testing.B) {
		benchmarkInspectSearch(b, All(Include(selective), Include(broad)), nil, entries, query, false, false, 4)
	})
	b.Run("InspectedChild", func(b *testing.B) {
		var inspector Inspector
		benchmarkInspectSearch(b, All(Include(selective), Inspect(&inspector, Include(broad))), inspector, entries, query, false, true, 4)
	})
}

func benchmarkInspectSearch(
	b *testing.B,
	schema Rule[inspectBenchmarkConstraint],
	inspector Inspector,
	entries func(func(inspectBenchmarkConstraint, int) bool),
	query inspectBenchmarkConstraint,
	local bool,
	forceObservation bool,
	want int,
) {
	b.Helper()
	index, err := New[inspectBenchmarkConstraint, int](schema).Build(entries)
	if err != nil {
		b.Fatal(err)
	}
	var dst []int
	search := index.Search
	var localSearch *Local[inspectBenchmarkConstraint, int]
	if local {
		localSearch = index.Local()
		search = localSearch.Search
	}
	if forceObservation {
		observed := newBitmapPool()
		search = func(value inspectBenchmarkConstraint, dst *[]int) bool {
			return index.search(value, dst, observed)
		}
	}
	for range 2 {
		dst = dst[:0]
		search(query, &dst)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dst = dst[:0]
		search(query, &dst)
	}
	b.StopTimer()
	if localSearch != nil {
		localSearch.Close()
	}
	if len(dst) != want {
		b.Fatalf("got %d matches, want %d", len(dst), want)
	}
	runtime.KeepAlive(inspector)
}
