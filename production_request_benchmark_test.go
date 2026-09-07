package ruleix_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

var productionRequestSink []productionBenchmarkID

type productionIndexMatcher struct {
	index *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID]
}

func (m *productionIndexMatcher) Match(q productionBenchmarkConstraint, dst *[]productionBenchmarkID) {
	m.index.Search(q, dst)
}

func TestProductionBenchmarkImplementationsProduceSameResults(t *testing.T) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema()).Build(ruleix.Zip(constraints, ids))
	require.NoError(t, err)
	implementations := []struct {
		name string
		m    productionRequestMatcher
	}{
		{"LinearNatural", &productionLinearMatcher{constraints: constraints, ids: ids}},
		{"LinearOptimized", &productionLinearMatcher{constraints: constraints, ids: ids, optimized: true}},
		{"HandwrittenBitmap", newProductionBitmapMatcher(constraints, ids)},
		{"RuleixIndex", &productionIndexMatcher{index}},
		{"RuleixLocal", &productionRuleixMatcher{index: index}},
	}
	for _, mode := range []string{"Independent", "Correlated"} {
		for _, workingSet := range []bool{true, false} {
			queries := productionRequestQueries(mode, workingSet)
			for qi, query := range queries {
				var expected []productionBenchmarkID
				implementations[0].m.Match(query, &expected)
				expected = productionSortedIDs(expected)
				for _, implementation := range implementations[1:] {
					var actual []productionBenchmarkID
					productionBeginRequest(implementation.m)
					implementation.m.Match(query, &actual)
					productionEndRequest(implementation.m)
					require.Equalf(t, expected, productionSortedIDs(actual), "%s/%t query %d: %s", mode, workingSet, qi, implementation.name)
				}
			}
		}
	}
}

func TestProductionRuleixLocalIsRequestScoped(t *testing.T) {
	constraints, ids := productionBenchmarkDataN(100)
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema()).Build(ruleix.Zip(constraints, ids))
	require.NoError(t, err)
	matcher := &productionRuleixMatcher{index: index}
	matcher.BeginRequest()
	require.NotNil(t, matcher.local)
	var matches []productionBenchmarkID
	matcher.Match(productionBenchmarkQuery(1), &matches)
	matcher.Match(productionBenchmarkQuery(2), &matches)
	matcher.EndRequest()
	require.Nil(t, matcher.local)
}

type productionMatcherFactory struct {
	name string
	new  func() productionRequestMatcher
}

func productionMatcherFactories(b testing.TB, entries int) ([]productionMatcherFactory, func()) {
	constraints, ids := productionBenchmarkDataN(entries)
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema()).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	bitmap := newProductionBitmapMatcher(constraints, ids)
	factories := []productionMatcherFactory{
		{"LinearNatural", func() productionRequestMatcher { return &productionLinearMatcher{constraints: constraints, ids: ids} }},
		{"LinearOptimized", func() productionRequestMatcher {
			return &productionLinearMatcher{constraints: constraints, ids: ids, optimized: true}
		}},
		{"HandwrittenBitmap", func() productionRequestMatcher { clone := *bitmap; return &clone }},
		{"RuleixIndex", func() productionRequestMatcher { return &productionIndexMatcher{index} }},
		{"RuleixLocal", func() productionRequestMatcher { return &productionRuleixMatcher{index: index} }},
	}
	return factories, func() {
		runtime.KeepAlive(index)
	}
}

func benchmarkProductionRequest(b *testing.B, matcher productionRequestMatcher, queries []productionBenchmarkConstraint, lookups int) {
	results := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		base := (i * lookups) % len(queries)
		productionBeginRequest(matcher)
		for j := range lookups {
			results = results[:0]
			matcher.Match(queries[(base+j)%len(queries)], &results)
			productionRequestSink = results
		}
		productionEndRequest(matcher)
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*lookups), "ns/lookup")
	b.ReportMetric(float64(b.N*lookups)/b.Elapsed().Seconds(), "lookups/s")
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "requests/s")
}

// BenchmarkRequest latest local smoke run (Apple M1 Max, Go 1.26.0,
// 38,098 constraints, Independent/LargeWorkingSet, 100ms): at 100 lookups,
// LinearNatural 47.0 ms/request, LinearOptimized 32.5 ms, HandwrittenBitmap
// 8.01 ms, RuleixIndex 4.45 ms, and request-scoped RuleixLocal 7.37 ms.
func BenchmarkRequest(b *testing.B) {
	factories, cleanup := productionMatcherFactories(b, productionBenchmarkEntries)
	b.Cleanup(cleanup)
	for _, mode := range []string{"Independent", "Correlated"} {
		for _, cache := range []struct {
			name string
			hot  bool
		}{{"HotContext", true}, {"LargeWorkingSet", false}} {
			queries := productionRequestQueries(mode, cache.hot)
			for _, factory := range factories {
				for _, lookups := range []int{1, 10, 25, 50, 100} {
					b.Run(fmt.Sprintf("%s/40K/%d/%s/%s", factory.name, lookups, mode, cache.name), func(b *testing.B) {
						benchmarkProductionRequest(b, factory.new(), queries, lookups)
					})
				}
			}
		}
	}
}

// BenchmarkRequestParallel latest local smoke run command:
// go test -run '^$' -bench '^BenchmarkRequestParallel/' -benchmem -benchtime=100ms -cpu=1,2,4,8 .
func BenchmarkRequestParallel(b *testing.B) {
	factories, cleanup := productionMatcherFactories(b, productionBenchmarkEntries)
	b.Cleanup(cleanup)
	queries := productionRequestQueries("Correlated", false)
	for _, factory := range factories {
		for _, lookups := range []int{10, 50, 100} {
			b.Run(fmt.Sprintf("%s/40K/%d", factory.name, lookups), func(b *testing.B) {
				type workerState struct {
					matcher productionRequestMatcher
					results []productionBenchmarkID
				}
				workers := make(chan workerState, runtime.GOMAXPROCS(0))
				for range runtime.GOMAXPROCS(0) {
					workers <- workerState{factory.new(), make([]productionBenchmarkID, 0, productionBenchmarkEntries)}
				}
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					worker := <-workers
					matcher, results, request := worker.matcher, worker.results, 0
					for pb.Next() {
						base := request * lookups % len(queries)
						productionBeginRequest(matcher)
						for j := range lookups {
							results = results[:0]
							matcher.Match(queries[(base+j)%len(queries)], &results)
						}
						productionEndRequest(matcher)
						request++
					}
					workers <- workerState{matcher, results}
				})
				b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "requests/s")
				b.ReportMetric(float64(b.N*lookups)/b.Elapsed().Seconds(), "lookups/s")
			})
		}
	}
}

func BenchmarkRequestScaling(b *testing.B) {
	queries := productionRequestQueries("Independent", false)
	for _, entries := range []int{10_000, productionBenchmarkEntries, 100_000, 1_000_000} {
		factories, cleanup := productionMatcherFactories(b, entries)
		for _, factory := range factories {
			b.Run(fmt.Sprintf("%s/%s", factory.name, productionMatcherName(entries)), func(b *testing.B) {
				benchmarkProductionRequest(b, factory.new(), queries, 1)
			})
		}
		cleanup()
	}
}

func BenchmarkSyntheticMixedRequest(b *testing.B) {
	factories, cleanup := productionMatcherFactories(b, productionBenchmarkEntries)
	b.Cleanup(cleanup)
	queries, mixture := productionRequestQueries("Correlated", false), []int{10, 25, 25, 25, 50, 50, 50, 75, 75, 100}
	for _, factory := range factories {
		b.Run(factory.name, func(b *testing.B) {
			matcher, results := factory.new(), make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				lookups, base := mixture[i%len(mixture)], i*100%len(queries)
				productionBeginRequest(matcher)
				for j := range lookups {
					results = results[:0]
					matcher.Match(queries[(base+j)%len(queries)], &results)
				}
				productionEndRequest(matcher)
			}
		})
	}
}
