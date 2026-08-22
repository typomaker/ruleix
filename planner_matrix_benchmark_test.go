package ruleix_test

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
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
// result, the normal selective query, and a wildcard-heavy query. Data setup
// and index construction stay outside the timed region.
func BenchmarkProductionScaleSearch(b *testing.B) {
	for _, entries := range productionScaleBenchmarkSizes() {
		b.Run(fmt.Sprintf("Rules%d", entries), func(b *testing.B) {
			constraints, ids := productionBenchmarkDataN(entries)
			index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
				productionBenchmarkSchema(),
			).Build(ruleix.Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}

			selective := productionBenchmarkQuery(100)
			small := productionBenchmarkQuery(100)
			small.platform.name = "missing"
			wildcardHeavy := productionBenchmarkQuery(100)
			wildcardHeavy.customerUUID = nil
			wildcardHeavy.storeUUID = nil
			wildcardHeavy.regionID = nil
			for _, workload := range []struct {
				name  string
				query productionBenchmarkConstraint
			}{
				{name: "Small", query: small},
				{name: "Selective", query: selective},
				{name: "WildcardHeavy", query: wildcardHeavy},
			} {
				b.Run(workload.name, func(b *testing.B) {
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

// BenchmarkProductionScaleBuild measures build time and allocation traffic at
// the same sizes as the search matrix.
func BenchmarkProductionScaleBuild(b *testing.B) {
	for _, entries := range productionScaleBenchmarkSizes() {
		b.Run(fmt.Sprintf("Rules%d", entries), func(b *testing.B) {
			constraints, ids := productionBenchmarkDataN(entries)
			distribution := productionBenchmarkPostingDistribution(constraints)
			builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
				productionBenchmarkSchema(),
			)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				index, err := builder.Build(ruleix.Zip(constraints, ids))
				if err != nil {
					b.Fatal(err)
				}
				productionBenchmarkIndexResult = index
			}
			b.ReportMetric(float64(entries), "rules/op")
			b.ReportMetric(float64(distribution.postings), "postings/index")
			b.ReportMetric(float64(distribution.memberships)/float64(distribution.postings), "IDs/posting")
			b.ReportMetric(float64(distribution.maxPosting), "max-IDs/posting")
			b.ReportMetric(100*float64(distribution.wildcards)/float64(distribution.memberships), "wildcard-%")
		})
	}
}

type productionPostingDistribution struct {
	postings    int
	memberships int
	maxPosting  int
	wildcards   int
}

// productionBenchmarkPostingDistribution describes the input distribution in
// the same units as the leaf indexes: one wildcard posting plus one posting per
// distinct concrete value. It deliberately measures logical postings rather
// than Roaring's physical containers, which may be interned during Build.
func productionBenchmarkPostingDistribution(constraints []productionBenchmarkConstraint) productionPostingDistribution {
	var result productionPostingDistribution
	collect := func(counts map[any]int, wildcards int) {
		result.postings += len(counts) + 1
		result.memberships += len(constraints)
		result.wildcards += wildcards
		result.maxPosting = max(result.maxPosting, wildcards)
		for _, count := range counts {
			result.maxPosting = max(result.maxPosting, count)
		}
	}
	values := func(value func(productionBenchmarkConstraint) (any, bool)) {
		counts := make(map[any]int)
		wildcards := 0
		for _, constraint := range constraints {
			if concrete, ok := value(constraint); ok {
				counts[concrete]++
			} else {
				wildcards++
			}
		}
		collect(counts, wildcards)
	}

	values(func(v productionBenchmarkConstraint) (any, bool) {
		if v.activity == nil {
			return nil, false
		}
		return v.activity.since.Truncate(time.Second), true
	})
	values(func(v productionBenchmarkConstraint) (any, bool) {
		if v.activity == nil {
			return nil, false
		}
		return v.activity.until.Truncate(time.Second), true
	})
	values(func(v productionBenchmarkConstraint) (any, bool) {
		if v.customerOrderCount == nil {
			return nil, false
		}
		return v.customerOrderCount.total, true
	})
	values(func(v productionBenchmarkConstraint) (any, bool) {
		if v.slotTime == nil {
			return nil, false
		}
		return v.slotTime.since.Truncate(time.Second), true
	})
	values(func(v productionBenchmarkConstraint) (any, bool) {
		if v.slotTime == nil {
			return nil, false
		}
		return v.slotTime.until.Truncate(time.Second), true
	})
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.customerUUID) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.customerSegment) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.customerFraud) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.storeUUID) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.deliveryAreaID) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.regionID) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.retailerUUID) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.vertical) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.slotType) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.slotDayOfWeek) })
	values(func(v productionBenchmarkConstraint) (any, bool) {
		if v.platform == nil {
			return nil, false
		}
		return v.platform.name, true
	})
	values(func(v productionBenchmarkConstraint) (any, bool) {
		if v.platform == nil || v.platform.version == nil {
			return nil, false
		}
		version := v.platform.version
		return [3]int{version.major, version.minor, version.patch}, true
	})
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.dbs) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.marketType) })
	values(func(v productionBenchmarkConstraint) (any, bool) { return benchmarkAnyOptional(v.abTest) })
	return result
}

func benchmarkAnyOptional[V any](value *V) (any, bool) {
	if value == nil {
		return nil, false
	}
	return *value, true
}

// BenchmarkProductionScaleRetainedMemory reports live index bytes. Use a
// fixed iteration count, preferably 1x for the 1M case, so every measured
// index can remain live through the final heap sample.
func BenchmarkProductionScaleRetainedMemory(b *testing.B) {
	for _, entries := range productionScaleBenchmarkSizes() {
		b.Run(fmt.Sprintf("Rules%d", entries), func(b *testing.B) {
			constraints, ids := productionBenchmarkDataN(entries)
			benchmarkProductionRetainedMemory(b, constraints, ids)
		})
	}
}

// BenchmarkProductionScaleBuildPeakMemory reports the heap growth while one
// index is built. GC is disabled around the measured build so temporary build
// allocations cannot disappear before the peak sample. Use a fixed iteration
// count; the benchmark performs a full build for every iteration.
func BenchmarkProductionScaleBuildPeakMemory(b *testing.B) {
	for _, entries := range productionScaleBenchmarkSizes() {
		b.Run(fmt.Sprintf("Rules%d", entries), func(b *testing.B) {
			constraints, ids := productionBenchmarkDataN(entries)
			builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
				productionBenchmarkSchema(),
			)
			if _, err := builder.Build(ruleix.Zip(constraints, ids)); err != nil {
				b.Fatal(err)
			}

			var peakTotal uint64
			for range b.N {
				runtime.GC()
				runtime.GC()
				var before, peak runtime.MemStats
				runtime.ReadMemStats(&before)
				previousGCPercent := debug.SetGCPercent(-1)
				index, err := builder.Build(ruleix.Zip(constraints, ids))
				runtime.ReadMemStats(&peak)
				debug.SetGCPercent(previousGCPercent)
				if err != nil {
					b.Fatal(err)
				}
				if peak.HeapAlloc > before.HeapAlloc {
					peakTotal += peak.HeapAlloc - before.HeapAlloc
				}
				runtime.KeepAlive(index)
			}
			b.ReportMetric(float64(peakTotal)/float64(b.N), "peak-heap-B/build")
		})
	}
}

// BenchmarkProductionScaleBuildGCPressure reports collections and stop-the-
// world pause time caused by a build with the process's normal GC settings.
// Run it with a fixed iteration count to keep the per-build samples explicit.
func BenchmarkProductionScaleBuildGCPressure(b *testing.B) {
	for _, entries := range productionScaleBenchmarkSizes() {
		b.Run(fmt.Sprintf("Rules%d", entries), func(b *testing.B) {
			constraints, ids := productionBenchmarkDataN(entries)
			builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
				productionBenchmarkSchema(),
			)
			if _, err := builder.Build(ruleix.Zip(constraints, ids)); err != nil {
				b.Fatal(err)
			}

			var collectionsTotal, pauseTotal uint64
			for range b.N {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				index, err := builder.Build(ruleix.Zip(constraints, ids))
				if err != nil {
					b.Fatal(err)
				}
				runtime.ReadMemStats(&after)
				collectionsTotal += uint64(after.NumGC - before.NumGC)
				pauseTotal += after.PauseTotalNs - before.PauseTotalNs
				runtime.KeepAlive(index)
			}
			b.ReportMetric(float64(collectionsTotal)/float64(b.N), "GC-cycles/build")
			b.ReportMetric(float64(pauseTotal)/float64(b.N), "GC-pause-ns/build")
		})
	}
}
