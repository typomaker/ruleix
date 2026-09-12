package ruleix_test

import (
	"runtime"
	"testing"

	"github.com/typomaker/ruleix"
)

const productionJoinQueryCount = 100

func productionJoinQueries() []productionBenchmarkConstraint {
	dimensions := [...]int{5, 5, 2, 2}
	coordinates := make([]int, len(dimensions))
	queries := make([]productionBenchmarkConstraint, 0, productionJoinQueryCount)
	var visit func(int, bool)
	visit = func(dimension int, reverse bool) {
		if dimension == len(dimensions) {
			query := productionBenchmarkQuery(100 + coordinates[0])
			query.platform = &productionBenchmarkPlatform{
				name: "paa", version: &productionBenchmarkVersion{major: 1 + coordinates[1]},
			}
			query.slotType = ptr(uint8(1 + coordinates[2]))
			query.dbs = ptr(coordinates[3] == 0)
			queries = append(queries, query)
			return
		}
		for offset := range dimensions[dimension] {
			coordinate := offset
			if reverse {
				coordinate = dimensions[dimension] - 1 - offset
			}
			coordinates[dimension] = coordinate
			childReverse := coordinate%2 == 1
			if reverse {
				childReverse = !childReverse
			}
			visit(dimension+1, childReverse)
		}
	}
	visit(0, false)
	return queries
}

func TestProductionJoinQueriesFollowSingleDimensionSteps(t *testing.T) {
	queries := productionJoinQueries()
	if len(queries) != productionJoinQueryCount {
		t.Fatalf("query count = %d, want %d", len(queries), productionJoinQueryCount)
	}
	for index := 1; index < len(queries); index++ {
		previous, current := queries[index-1], queries[index]
		changes := 0
		if !previous.activity.since.Equal(current.activity.since) {
			changes++
		}
		if previous.platform.version.major != current.platform.version.major {
			changes++
		}
		if *previous.slotType != *current.slotType {
			changes++
		}
		if *previous.dbs != *current.dbs {
			changes++
		}
		if changes != 1 {
			t.Fatalf("queries %d and %d change %d dimensions, want 1", index-1, index, changes)
		}
	}
}

// BenchmarkProductionJoinLocal models one joined Cartesian batch of 100
// queries. Adjacent queries change one of four dimensions while reusing the
// other three. Run with GOMAXPROCS=1 and compare identical copies of this file.
func BenchmarkProductionJoinLocal(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := productionJoinQueries()
	searchBatch := func(
		search func(productionBenchmarkConstraint, *[]productionBenchmarkID) bool,
		matches *[]productionBenchmarkID,
	) {
		for _, query := range queries {
			*matches = (*matches)[:0]
			search(query, matches)
		}
		runtime.KeepAlive(*matches)
	}

	b.Run("IndexBatch100", func(b *testing.B) {
		matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
		b.ReportAllocs()
		for range b.N {
			searchBatch(index.Search, &matches)
		}
	})
	b.Run("LocalColdBatch100", func(b *testing.B) {
		matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
		b.ReportAllocs()
		for range b.N {
			local := index.Local()
			searchBatch(local.Search, &matches)
			local.Close()
		}
	})
	b.Run("LocalWarmBatch100", func(b *testing.B) {
		local := index.Local()
		b.Cleanup(local.Close)
		matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
		searchBatch(local.Search, &matches)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			searchBatch(local.Search, &matches)
		}
	})
}

// BenchmarkProductionJoinLocalRetainedMemory reports the live heap retained by
// one Local after its complete 100-query Cartesian batch has been cached.
func BenchmarkProductionJoinLocalRetainedMemory(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := productionJoinQueries()
	runtime.GC()
	runtime.GC()
	before := heapAlloc()
	locals := make([]*ruleix.Local[productionBenchmarkConstraint, productionBenchmarkID], b.N)
	for iteration := range b.N {
		local := index.Local()
		var matches []productionBenchmarkID
		for _, query := range queries {
			matches = matches[:0]
			local.Search(query, &matches)
		}
		locals[iteration] = local
	}
	runtime.GC()
	runtime.GC()
	after := heapAlloc()
	b.ReportMetric(float64(after-before)/float64(b.N), "retained-B/Local")
	for _, local := range locals {
		local.Close()
	}
	runtime.KeepAlive(index)
}
