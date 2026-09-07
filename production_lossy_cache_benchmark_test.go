package ruleix_test

import (
	"math"
	"runtime"
	"testing"

	"github.com/typomaker/ruleix"
)

// BenchmarkProductionShapeLossyLocalRetainedMemory measures the incremental
// live heap of a two-query warm Lossy Local. Apple M1 Max, Go 1.26.0,
// GOMAXPROCS=1, 20x x5: the 512-ID candidate retained 96,450-96,478
// B/Local versus 93,388-93,404 B/Local at ab24d5a.
//
//	GOMAXPROCS=1 go test -run '^$' \
//	  -bench '^BenchmarkProductionShapeLossyLocalRetainedMemory$' \
//	  -benchmem -benchtime=20x -count=5 .
func BenchmarkProductionShapeLossyLocalRetainedMemory(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	var inspector ruleix.Inspector
	_, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](ruleix.Inspect(
		&inspector,
		ruleix.Lossy(productionBenchmarkSchema(), ruleix.MemoryLimit(math.MaxUint64)),
	)).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	exactBytes, ok := inspector.Snapshot().MemoryUsage()
	if !ok {
		b.Fatal("exact production memory usage is unavailable")
	}
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		ruleix.Lossy(productionBenchmarkSchema(), ruleix.MemoryLimit(exactBytes/2)),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100), productionBenchmarkQuery(101),
	}
	locals := make([]*ruleix.Local[productionBenchmarkConstraint, productionBenchmarkID], b.N)
	matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
	runtime.GC()
	runtime.GC()
	before := heapAlloc()

	b.ResetTimer()
	for i := range b.N {
		local := index.Local()
		for range 6 {
			for _, query := range queries {
				matches = matches[:0]
				local.Search(query, &matches)
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
}
