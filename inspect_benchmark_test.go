package ruleix

import "testing"

type inspectBenchmarkConstraint struct {
	selective int
	broad     int
}

// BenchmarkInspectRuntimeOverhead compares equivalent compiled trees with and
// without explicitly enabled runtime observation. The leaf cases measure the
// materialization wrapper directly; the All cases also cover candidate checks
// and the specialized top-level append path.
//
// Apple M1 Max, 2026-08-24, medians of five 300 ms runs: warm Local leaf
// 41.70 ns/op plain and 49.91 ns/op inspected; warm Local All 173.2 ns/op plain
// and 176.6 ns/op inspected. Reproduce with: go test -run '^$' -bench
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
				benchmarkInspectSearch(b, Include(selective), nil, entries, query, local)
			})
			b.Run("InspectedLeaf", func(b *testing.B) {
				var inspector Inspector
				benchmarkInspectSearch(b, Inspect(&inspector, Include(selective)), inspector, entries, query, local)
			})
			b.Run("All", func(b *testing.B) {
				benchmarkInspectSearch(b, All(Include(selective), Include(broad)), nil, entries, query, local)
			})
			b.Run("InspectedAll", func(b *testing.B) {
				var inspector Inspector
				benchmarkInspectSearch(b, Inspect(&inspector, All(Include(selective), Include(broad))), inspector, entries, query, local)
			})
			b.Run("AllInspectedBroadChild", func(b *testing.B) {
				var inspector Inspector
				benchmarkInspectSearch(b, All(Include(selective), Inspect(&inspector, Include(broad))), inspector, entries, query, local)
			})
		})
	}
}

func benchmarkInspectSearch(
	b *testing.B,
	schema Rule[inspectBenchmarkConstraint],
	inspector Inspector,
	entries func(func(inspectBenchmarkConstraint, int) bool),
	query inspectBenchmarkConstraint,
	local bool,
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
	if len(dst) != 1 {
		b.Fatalf("got %d matches, want 1", len(dst))
	}
	if inspector != nil {
		snapshot := inspector.Snapshot()
		if snapshot.Search() == 0 && snapshot.CandidateCheck() == 0 {
			b.Fatal("inspector did not observe benchmark execution")
		}
	}
}
