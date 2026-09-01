package ruleix_test

import (
	"cmp"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/typomaker/ruleix"
)

var productionScaleBenchmarkMatches []productionBenchmarkID

const productionLargeScaleEnv = "RULEIX_BENCHMARK_LARGE"

func productionScaleBenchmarkSizes() []int {
	sizes := []int{10_000, 100_000, 1_000_000}
	if os.Getenv(productionLargeScaleEnv) != "" {
		sizes = append(sizes, 5_000_000, 10_000_000)
	}
	return sizes
}

// BenchmarkProductionScaleSearch extends the production-shaped baseline to
// the rule counts required by the planner roadmap. Each size covers a small
// result, the normal selective query, a wildcard-heavy query, and dedicated
// large-result, nested-All, and range-heavy planner shapes. Data setup and
// index construction stay outside the timed region.
func BenchmarkProductionScaleSearch(b *testing.B) {
	for _, entries := range productionScaleBenchmarkSizes() {
		b.Run(fmt.Sprintf("Rules%d", entries), func(b *testing.B) {
			constraints, ids := productionBenchmarkDataN(entries)
			rangeConstraints := append([]productionBenchmarkConstraint(nil), constraints...)
			selective := productionBenchmarkQuery(100)
			small := productionBenchmarkQuery(100)
			small.platform.name = "missing"
			wildcardHeavy := productionBenchmarkQuery(100)
			wildcardHeavy.customerUUID = nil
			wildcardHeavy.storeUUID = nil
			wildcardHeavy.regionID = nil
			baseTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
			for i := range rangeConstraints {
				day := i % 365
				rangeConstraints[i].slotTime = &productionBenchmarkTimeRange{
					since: baseTime.AddDate(0, 0, day),
					until: baseTime.AddDate(0, 0, day+14),
				}
			}
			rangeHeavy := productionBenchmarkConstraint{
				activity:           &productionBenchmarkTimeRange{since: baseTime.AddDate(0, 0, 100)},
				slotTime:           &productionBenchmarkTimeRange{since: baseTime.AddDate(0, 0, 105)},
				customerOrderCount: &productionBenchmarkOrderCount{total: 1},
			}
			largeResult := productionBenchmarkConstraint{
				customerOrderCount: &productionBenchmarkOrderCount{total: 1},
				marketType:         ptr(uint8(1)),
			}

			workloads := []struct {
				name        string
				schema      ruleix.Rule[productionBenchmarkConstraint]
				constraints []productionBenchmarkConstraint
				query       productionBenchmarkConstraint
			}{
				{name: "Small", schema: productionBenchmarkSchema(), constraints: constraints, query: small},
				{name: "Selective", schema: productionBenchmarkSchema(), constraints: constraints, query: selective},
				{name: "WildcardHeavy", schema: productionBenchmarkSchema(), constraints: constraints, query: wildcardHeavy},
				{name: "LargeResult", schema: productionBenchmarkLargeResultSchema(), constraints: constraints, query: largeResult},
				{name: "NestedAll", schema: productionBenchmarkNestedSchema(), constraints: constraints, query: selective},
				{name: "RangeHeavy", schema: productionBenchmarkRangeSchema(), constraints: rangeConstraints, query: rangeHeavy},
			}
			for _, workload := range workloads {
				b.Run(workload.name, func(b *testing.B) {
					index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
						workload.schema,
					).Build(ruleix.Zip(workload.constraints, ids))
					if err != nil {
						b.Fatal(err)
					}
					matches := make([]productionBenchmarkID, 0, entries)
					index.Search(workload.query, &matches)
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						matches = matches[:0]
						index.Search(workload.query, &matches)
					}
					b.ReportMetric(float64(len(matches)), "matches/op")
					productionScaleBenchmarkMatches = matches
				})
			}
		})
	}
}

func productionBenchmarkLargeResultSchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		ruleix.CompareBy(func(value productionBenchmarkConstraint) (int, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return value.customerOrderCount.total, true
		}, func(value productionBenchmarkConstraint) (ruleix.Operator, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return ruleix.OperatorGTE, true
		}, cmp.Compare[int]),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(value.marketType)
		}),
	)
}

func productionBenchmarkNestedSchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		productionBenchmarkActivityRule(),
		ruleix.All(
			ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
				return benchmarkOptional(value.customerUUID)
			}),
			ruleix.All(
				ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
					return benchmarkOptional(value.slotType)
				}),
				productionBenchmarkPlatformRule(),
			),
		),
	)
}

func productionBenchmarkRangeSchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		productionBenchmarkActivityRule(),
		productionBenchmarkSlotTimeRule(),
		ruleix.CompareBy(func(value productionBenchmarkConstraint) (int, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return value.customerOrderCount.total, true
		}, func(value productionBenchmarkConstraint) (ruleix.Operator, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return ruleix.OperatorGTE, true
		}, cmp.Compare[int]),
	)
}
