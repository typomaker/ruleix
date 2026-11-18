//nolint:lll // Production-shaped getters stay inline for benchmark fidelity.
package ruleix_test

import (
	"runtime"
	"sync"
	"testing"

	"github.com/typomaker/ruleix"
)

// BenchmarkProductionShapeEqualityOnlySearch last local run (Apple M1 Max):
// go test -run '^$' -bench '^BenchmarkProductionShapeEqualityOnlySearch$' -benchmem -benchtime=500ms -count=5 .
// v0.8.2/current medians at 300ms x5: Index 15,579/15,430 ns/op; Local
// 222.5/222.8 ns/op. Allocation classes remain 20/0.
func BenchmarkProductionShapeEqualityOnlySearch(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkEqualityOnlySchema(),
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
				query := queries[i%len(queries)]
				matches = matches[:0]
				if local {
					searcher.SearchOne(query, &matches)
				} else {
					index.SearchOne(query, &matches)
				}
			}
		})
	}
}

// BenchmarkProductionShapeParallelLocalBatch100 last local run (Apple M1 Max):
// go test -run '^$' -bench '^BenchmarkProductionShapeParallelLocalBatch100$' -benchmem -benchtime=500ms -count=5 .
// Median: 46,506 ns/op, 465.1 ns/search, 370 B/op, 1 alloc/op.
func BenchmarkProductionShapeParallelLocalBatch100(b *testing.B) {
	const (
		workers           = 5
		searchesPerWorker = 20
		searchesPerBatch  = workers * searchesPerWorker
	)
	type job struct {
		queries []productionBenchmarkConstraint
		done    *sync.WaitGroup
	}

	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}

	queries := make([]productionBenchmarkConstraint, searchesPerBatch)
	for i := range queries {
		queries[i] = productionBenchmarkQuery(100 + i%2)
	}

	jobs := make([]chan job, workers)
	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for worker := range workers {
		jobs[worker] = make(chan job, 1)
		go func(jobs <-chan job) {
			defer workersDone.Done()
			local := index.TestLocal()
			defer local.Close()
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			for work := range jobs {
				for _, query := range work.queries {
					matches = matches[:0]
					local.SearchOne(query, &matches)
				}
				work.done.Done()
			}
		}(jobs[worker])
	}
	defer func() {
		for _, workerJobs := range jobs {
			close(workerJobs)
		}
		workersDone.Wait()
	}()

	b.ReportAllocs()
	b.ReportMetric(searchesPerBatch, "searches/op")
	b.ResetTimer()
	for range b.N {
		var batchDone sync.WaitGroup
		batchDone.Add(workers)
		for worker, workerJobs := range jobs {
			from := worker * searchesPerWorker
			workerJobs <- job{
				queries: queries[from : from+searchesPerWorker],
				done:    &batchDone,
			}
		}
		batchDone.Wait()
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*searchesPerBatch), "ns/search")
}

func BenchmarkProductionShapeBuild(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index, err := builder.Build(ruleix.Zip(constraints, ids))
		if err != nil {
			b.Fatal(err)
		}
		productionBenchmarkIndexResult = index
	}
	b.ReportMetric(productionBenchmarkEntries, "entries/op")
}

// BenchmarkProductionShapeRetainedMemory measures the live heap owned by a
// completed index rather than the allocation traffic reported by B/op. Run it
// with a fixed iteration count so all indexes can remain live until the final
// heap sample, for example:
//
//	go test -run '^$' -bench '^BenchmarkProductionShapeRetainedMemory$' -benchtime=5x -count=5
func BenchmarkProductionShapeRetainedMemory(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	benchmarkProductionRetainedMemory(b, constraints, ids)
}

// BenchmarkProductionShapeShuffledRetainedMemory removes insertion-order
// locality while preserving the same production field distribution.
func BenchmarkProductionShapeShuffledRetainedMemory(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	state := uint64(0x9e3779b97f4a7c15)
	for i := len(constraints) - 1; i > 0; i-- {
		state = state*6364136223846793005 + 1442695040888963407
		j := int(state % uint64(i+1))
		constraints[i], constraints[j] = constraints[j], constraints[i]
		ids[i], ids[j] = ids[j], ids[i]
	}
	benchmarkProductionRetainedMemory(b, constraints, ids)
}

func benchmarkProductionRetainedMemory(
	b *testing.B,
	constraints []productionBenchmarkConstraint,
	ids []productionBenchmarkID,
) {
	builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema())
	// Prime reusable Builder hints before the baseline so their retained memory
	// is not incorrectly attributed to the measured indexes.
	if _, err := builder.Build(ruleix.Zip(constraints, ids)); err != nil {
		b.Fatal(err)
	}
	indexes := make([]*ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID], b.N)

	runtime.GC()
	runtime.GC() // A second cycle also drops objects retained by sync.Pool.
	before := heapAlloc()

	b.ResetTimer()
	for i := range b.N {
		index, err := builder.Build(ruleix.Zip(constraints, ids))
		if err != nil {
			b.Fatal(err)
		}
		indexes[i] = index
	}
	b.StopTimer()

	runtime.GC()
	runtime.GC()
	after := heapAlloc()
	runtime.KeepAlive(constraints)
	runtime.KeepAlive(ids)
	runtime.KeepAlive(builder)
	runtime.KeepAlive(indexes)

	var retained uint64
	if after > before {
		retained = after - before
	}
	perIndex := float64(retained) / float64(b.N)
	b.ReportMetric(perIndex, "retained-B/index")
	b.ReportMetric(perIndex/float64(len(constraints)), "retained-B/ID")
}

// BenchmarkProductionShapeLocalRetainedMemory measures the incremental live
// heap of one Local while keeping its shared Index and caller-owned result
// storage in the baseline. Warm alternates the same two queries as the search
// benchmark, while Adaptive cycles four queries to exercise cache growth.
// Last local run (Apple M1 Max):
// go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' -benchmem -benchtime=3x -count=3 .
// Cold: 2,968; Warm: 91,003; Adaptive: 107,845; Adversarial: 73,984
// retained-B/local.
//
//nolint:gocognit // The retained-memory cases must share identical fixture construction.
func BenchmarkProductionShapeLocalRetainedMemory(b *testing.B) {
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
		productionBenchmarkQuery(102),
		productionBenchmarkQuery(103),
	}

	for _, mode := range []struct {
		name    string
		queries int
	}{{"Cold", 0}, {"Warm", 2}, {"Adaptive", 4}, {"Adversarial", -1}} {
		name := mode.name
		b.Run(name, func(b *testing.B) {
			locals := make([]*ruleix.TestContext[productionBenchmarkConstraint, productionBenchmarkID], b.N)
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			runtime.GC()
			runtime.GC()
			before := heapAlloc()

			b.ResetTimer()
			for i := range b.N {
				local := index.TestLocal()
				if mode.queries < 0 {
					for value := 100; value < 356; value++ {
						matches = matches[:0]
						local.SearchOne(productionBenchmarkQuery(value), &matches)
					}
				} else if mode.queries > 0 {
					for range 6 {
						for _, query := range queries[:mode.queries] {
							matches = matches[:0]
							local.SearchOne(query, &matches)
						}
					}
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
		})
	}
}

func heapAlloc() uint64 {
	var statistics runtime.MemStats
	runtime.ReadMemStats(&statistics)
	return statistics.HeapAlloc
}
