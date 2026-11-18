//nolint:lll // Production-shaped getters stay inline for benchmark fidelity.
package ruleix_test

import (
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/typomaker/ruleix"
)

// BenchmarkProductionShapeLossySearch compares the complete production schema
// at its exact representation and a 50% aggregate retained-memory budget. The
// setup builds both indexes outside the timed region and rotates the same two
// queries used by BenchmarkProductionShapeSearch.
//
// Reproduce with:
//
//	GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkProductionShapeLossySearch/' \
//	  -benchmem -benchtime=1s -count=5 .
//
// Latest build-time key-rebuilding run (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1,
// 38,098 entries, 377,122-byte budget, 500ms x5): Index median 43,075 ns/op,
// 38,909 B/op, 22 allocs/op; Local median 586.9 ns/op, 0 B/op and 0 allocs/op;
// both rotating queries returned 139 candidates. The preceding streaming
// medians were 50,386 ns/op and 3,893 ns/op respectively.
func BenchmarkProductionShapeLossySearch(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	var inspector ruleix.Inspector
	_, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](ruleix.Inspect(
		&inspector,
		ruleix.Lossy(productionBenchmarkSchema(), ruleix.MemoryLimit(^uint64(0))),
	)).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	exactBytes, ok := inspector.Snapshot().MemoryUsage()
	if !ok {
		b.Fatal("exact production memory usage is unavailable")
	}
	budget := exactBytes / 2
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		ruleix.Lossy(productionBenchmarkSchema(), ruleix.MemoryLimit(budget)),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for _, local := range []bool{false, true} {
		name := "Index"
		if local {
			name = "Local"
		}
		b.Run(name, func(b *testing.B) {
			searcher := index.TestLocal()
			b.Cleanup(searcher.Close)
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				matches = matches[:0]
				if local {
					searcher.SearchOne(queries[i%len(queries)], &matches)
				} else {
					index.SearchOne(queries[i%len(queries)], &matches)
				}
			}
			b.ReportMetric(float64(budget), "budget-bytes")
			b.ReportMetric(float64(len(matches)), "candidates/op")
		})
	}
}

// The benchmark drives one search worker. Production code must retain the
// documented one-Local-per-goroutine rule rather than sharing this holder's
// Local between concurrent readers.
type productionBenchmarkPublishedLocal struct {
	mu    sync.RWMutex
	local *ruleix.TestContext[productionBenchmarkConstraint, productionBenchmarkID]
}

func (published *productionBenchmarkPublishedLocal) replace(
	next *ruleix.TestContext[productionBenchmarkConstraint, productionBenchmarkID],
) *ruleix.TestContext[productionBenchmarkConstraint, productionBenchmarkID] {
	published.mu.Lock()
	previous := published.local
	published.local = next
	published.mu.Unlock()
	return previous
}

func (published *productionBenchmarkPublishedLocal) search(
	query productionBenchmarkConstraint,
	matches *[]productionBenchmarkID,
) bool {
	published.mu.RLock()
	matched := published.local.SearchOne(query, matches)
	published.mu.RUnlock()
	return matched
}

// BenchmarkProductionShapeLocalAfterSequentialBuilds detects generation-based
// Local.SearchOne degradation while repeatedly building and publishing independent
// indexes. Build is outside every timed region. A replacement creates the next
// Local before taking the write lock, swaps it under the lock, and closes the
// previous Local after releasing the lock, matching the production lifecycle.
// Direct isolates Local.SearchOne; Published also includes the uncontended read
// lock used to load the current Local.
//
// Reproduce with:
//
//	GOMAXPROCS=1 go test -run '^$' \
//	  -bench '^BenchmarkProductionShapeLocalAfterSequentialBuilds/' \
//	  -benchmem -benchtime=500ms -count=5 .
//
// Latest local run (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1): generation 1 to
// 64 Direct medians 227.0-228.5 ns/op; Published medians 234.2-235.3 ns/op;
// every generation remained at 0 B/op and 0 allocs/op.
func BenchmarkProductionShapeLocalAfterSequentialBuilds(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	)
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	var published productionBenchmarkPublishedLocal
	var current *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID]
	b.Cleanup(func() {
		if previous := published.replace(nil); previous != nil {
			previous.Close()
		}
		runtime.KeepAlive(current)
	})

	built := 0
	for _, generation := range [...]int{1, 2, 4, 8, 16, 32, 64} {
		for built < generation {
			next, err := builder.Build(ruleix.Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			nextLocal := next.TestLocal()
			previous := published.replace(nextLocal)
			if previous != nil {
				previous.Close()
			}
			current = next
			built++
		}

		local := published.local
		matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
		for warm := range 4 {
			matches = matches[:0]
			published.search(queries[warm%len(queries)], &matches)
		}
		if len(matches) == 0 {
			b.Fatalf("generation %d: warm search returned no matches", generation)
		}

		b.Run(fmt.Sprintf("Generation%02d/Direct", generation), func(b *testing.B) {
			b.ReportAllocs()
			for i := range b.N {
				matches = matches[:0]
				local.SearchOne(queries[i%len(queries)], &matches)
			}
		})
		b.Run(fmt.Sprintf("Generation%02d/Published", generation), func(b *testing.B) {
			b.ReportAllocs()
			for i := range b.N {
				matches = matches[:0]
				published.search(queries[i%len(queries)], &matches)
			}
		})
		runtime.KeepAlive(current)
	}
}

// BenchmarkProductionShapeExactCacheEntryHit isolates lookup of each of the
// two exact-query cache entries. The warm-up alternates the production queries
// so query 100 occupies the first entry and query 101 the second entry.
//
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkProductionShapeExactCacheEntryHit/' -benchmem -benchtime=1s -count=7 .
func BenchmarkProductionShapeExactCacheEntryHit(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for entry, query := range queries {
		name := "FirstEntry"
		if entry == 1 {
			name = "SecondEntry"
		}
		b.Run(name, func(b *testing.B) {
			local := index.TestLocal()
			b.Cleanup(local.Close)
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			for warm := range 4 {
				matches = matches[:0]
				local.SearchOne(queries[warm%len(queries)], &matches)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				matches = matches[:0]
				local.SearchOne(query, &matches)
			}
		})
	}
}

// BenchmarkProductionShapeLocalClose measures a complete short-lived Local
// lifecycle: acquire a context, warm its two-value working set, and return the
// context to the Index. This keeps cache teardown and subsequent resource reuse
// visible instead of amortizing them across a long-lived Local.
// Last local run (Apple M1 Max): 298,957 ns/op, 293,341 B/op, 110 allocs/op.
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkProductionShapeLocalClose$' -benchmem -benchtime=1s -count=5 .
func BenchmarkProductionShapeLocalClose(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)

	b.ReportAllocs()
	b.ReportMetric(float64(len(queries)*3), "searches/op")
	b.ResetTimer()
	for range b.N {
		local := index.TestLocal()
		for range 3 {
			for _, query := range queries {
				matches = matches[:0]
				local.SearchOne(query, &matches)
			}
		}
		local.Close()
	}
}
