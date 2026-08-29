package ruleix

import (
	"fmt"
	"testing"
)

// BenchmarkPhysicalIdentityAB keeps both executor modes in one binary and
// exercises the public root All path. BaselineFirst and IntegratedFirst make
// order effects visible without comparing measurements from separate builds.
// Counters are reported beside timing so later integrated work must explain
// any win by reduced physical operations.
//
// Latest local calibration (Apple M1 Max, Go 1.26.0, 2s, count=10), children
// 2/4/8: Baseline medians 1331/2226/4090 ns/op at 536 B/op and 2 allocs/op;
// Integrated medians 834/922/1087 ns/op at 0 B/op and 0 allocs/op. Baseline
// performs 2/4/8 physical searches and 1/3/7 intersections; Integrated performs
// one physical search, 2/4/8 mask tests, and skips 1/3/7 operands. This is
// structural evidence only; the complete performance decision remains step E.
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkPhysicalIdentityAB/' \
//	  -benchmem -benchtime=2s -count=10 .
func BenchmarkPhysicalIdentityAB(b *testing.B) {
	for _, children := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("Children%d", children), func(b *testing.B) {
			for _, order := range []struct {
				name  string
				modes []identityExecutionMode
			}{
				{"BaselineFirst", []identityExecutionMode{identityBaseline, identityIntegrated}},
				{"IntegratedFirst", []identityExecutionMode{identityIntegrated, identityBaseline}},
			} {
				b.Run(order.name, func(b *testing.B) {
					for _, mode := range order.modes {
						name := "Baseline"
						if mode == identityIntegrated {
							name = "Integrated"
						}
						b.Run(name, func(b *testing.B) {
							schema, constraints, ids := identityABFixture(children, false)
							harness := buildIdentityABIndex(b, mode, schema, constraints, ids)
							query := identityABConstraint{}
							for child := range children {
								query.values[child] = 1
							}
							result := make([]int, 0, 256)
							b.ReportAllocs()
							b.ResetTimer()
							for range b.N {
								result = result[:0]
								harness.index.Search(query, &result)
							}
							b.StopTimer()
							if len(result) != 256 {
								b.Fatalf("got %d results, want 256", len(result))
							}
							n := float64(b.N)
							b.ReportMetric(float64(harness.counters.materializations)/n, "materializations/op")
							b.ReportMetric(float64(harness.counters.intersections)/n, "intersections/op")
							b.ReportMetric(float64(harness.counters.containsChecks)/n, "contains/op")
							b.ReportMetric(float64(harness.counters.skippedOperands)/n, "skipped/op")
							b.ReportMetric(float64(harness.counters.maskTests)/n, "mask-tests/op")
							b.ReportMetric(float64(harness.counters.physicalInspectorSearches)/n, "physical-searches/op")
							if harness.counters.linearEqualityDedupRuns != 0 {
								b.Fatal("root All unexpectedly ran nested linear equality deduplication")
							}
						})
					}
				})
			}
		})
	}
}
