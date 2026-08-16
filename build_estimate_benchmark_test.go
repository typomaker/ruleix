package ruleix

import "testing"

const buildEstimateEntries = 10_000

type buildEstimateConstraint struct{ value *int }

func BenchmarkBuildSizeEstimate(b *testing.B) {
	values := make([]int, buildEstimateEntries)
	entries := func(yield func(buildEstimateConstraint, int) bool) {
		for i := range values {
			values[i] = i
			if !yield(buildEstimateConstraint{value: &values[i]}, i) {
				return
			}
		}
	}
	schema := Include(func(value buildEstimateConstraint) *int { return value.value })
	_, measured, err := buildIndex[buildEstimateConstraint, int](schema, entries, true, nil)
	if err != nil {
		b.Fatal(err)
	}

	globalHistory := buildStatistics{uniqueIDs: measured.uniqueIDs}
	// buildIndex accepts historical sizes and adds 5%. Translate the exact
	// estimate into that internal representation so this benchmark can measure
	// a sized-build candidate without exposing an experimental public API.
	exactUniqueIDHint := (buildEstimateEntries*20 + 20) / 21
	explicit := buildStatistics{uniqueIDs: exactUniqueIDHint}
	explicitWithNodeHistory := measured
	explicitWithNodeHistory.uniqueIDs = exactUniqueIDHint

	benchmarks := []struct {
		name  string
		hints *buildStatistics
	}{
		{name: "OneShot"},
		{name: "HistoricalGlobal", hints: &globalHistory},
		{name: "HistoricalPerNode", hints: &measured},
		{name: "ExplicitUniqueIDs", hints: &explicit},
		{name: "ExplicitUniqueIDsWithNodeHistory", hints: &explicitWithNodeHistory},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := buildIndex[buildEstimateConstraint, int](schema, entries, false, benchmark.hints); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(buildEstimateEntries, "entries/op")
		})
	}
}
