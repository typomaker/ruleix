package ruleix

import (
	"fmt"
	"math"
	"runtime"
	"testing"
)

func strictAntonymBenchmarkFixture(entries int) ([]strictAntonymConstraint, []int) {
	constraints := make([]strictAntonymConstraint, entries)
	ids := make([]int, entries)
	for id := range constraints {
		ids[id] = id
		if id < entries/2 {
			constraints[id].id = antonymPointer(id % 128)
		} else {
			constraints[id].name = antonymPointer(fmt.Sprintf("name-%d", id%128))
		}
	}
	return constraints, ids
}

// BenchmarkStrictEqualityAntonymLocalRetainedMemory measures the complete
// live-heap increment of a warmed Local. Apple M1 Max, Go 1.26.0,
// GOMAXPROCS=1, 20x x5 results are recorded in docs/performance-history.md.
func BenchmarkStrictEqualityAntonymLocalRetainedMemory(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		variant := "Baseline"
		if enabled {
			variant = "Antonym"
		}
		b.Run(variant, func(b *testing.B) {
			index := buildStrictAntonymBenchmarkIndex(b, false, enabled)
			locals := make([]*Local[strictAntonymConstraint, int], b.N)
			matches := make([]int, 0, 32)
			query := strictAntonymConstraint{id: antonymPointer(1), name: antonymPointer("absent")}
			runtime.GC()
			runtime.GC()
			before := antonymHeapAlloc()
			for i := range b.N {
				local := index.Local()
				for range 6 {
					matches = matches[:0]
					local.Search(query, &matches)
				}
				locals[i] = local
			}
			runtime.GC()
			runtime.GC()
			after := antonymHeapAlloc()
			runtime.KeepAlive(index)
			runtime.KeepAlive(locals)
			runtime.KeepAlive(matches)
			if after > before {
				b.ReportMetric(float64(after-before)/float64(b.N), "retained-B/local")
			}
			for _, local := range locals {
				local.Close()
			}
		})
	}
}

func antonymHeapAlloc() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// BenchmarkStrictEqualityAntonymBuild isolates the quadratic sibling scan
// added after bitmap interning. Apple M1 Max, Go 1.26.0, GOMAXPROCS=1,
// 20x x5 results are recorded in docs/performance-history.md.
func BenchmarkStrictEqualityAntonymBuild(b *testing.B) {
	constraints, ids := strictAntonymBenchmarkFixture(4096)
	for _, enabled := range []bool{false, true} {
		variant := "Baseline"
		if enabled {
			variant = "Antonym"
		}
		b.Run(variant, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				index, _, err := buildIndexPhysicalAliases(
					strictAntonymSchema(), Zip(constraints, ids), false, nil,
					buildOptions{compilePhysicalAliases: true, compileStrictAntonyms: enabled, enableStreaming: true},
				)
				if err != nil {
					b.Fatal(err)
				}
				runtime.KeepAlive(index)
			}
		})
	}
}

func buildStrictAntonymBenchmarkIndex(
	b *testing.B, compressed, enabled bool,
) *Index[strictAntonymConstraint, int] {
	b.Helper()
	constraints, ids := strictAntonymBenchmarkFixture(4096)
	var schema Rule[strictAntonymConstraint] = strictAntonymSchema()
	if compressed {
		schema = Lossy(schema, MemoryLimit(math.MaxUint64))
	}
	index, _, err := buildIndexPhysicalAliases(
		schema, Zip(constraints, ids), false, nil,
		buildOptions{compilePhysicalAliases: true, compileStrictAntonyms: enabled, enableStreaming: true},
	)
	if err != nil {
		b.Fatal(err)
	}
	return index
}

// BenchmarkStrictEqualityAntonymSearch compares identical physical indexes
// with and without the Build-compiled complement-pair metadata. The rotating
// absent keys exceed the Local result slots, keeping the combined operation
// observable instead of benchmarking only the final query-result cache.
// Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 4,096 entries, 1s x5 medians:
// Exact Index/rotating Local improved 4,113/4,395 ns to 423/565.5 ns;
// identity-compressed improved 4,181/4,237 ns to 436.5/570.5 ns. A later
// interleaved stable gate measured 70.64/70.67 ns baseline and 69.83/69.88 ns
// compiled. Index allocation classes changed from 14,385 B/5 to 80 B/4;
// rotating Local changed from 14,385 B/5 to 120 B/6.
func BenchmarkStrictEqualityAntonymSearch(b *testing.B) {
	for _, compressed := range []bool{false, true} {
		mode := "Exact"
		if compressed {
			mode = "IdentityCompressed"
		}
		for _, enabled := range []bool{false, true} {
			variant := "Baseline"
			if enabled {
				variant = "Antonym"
			}
			for _, path := range []string{"Index", "LocalRotating", "LocalStable"} {
				b.Run(mode+"/"+variant+"/"+path, func(b *testing.B) {
					index := buildStrictAntonymBenchmarkIndex(b, compressed, enabled)
					searcher := index.Local()
					b.Cleanup(searcher.Close)
					queries := make([]strictAntonymConstraint, 8)
					for i := range queries {
						queries[i] = strictAntonymConstraint{
							id: antonymPointer(i), name: antonymPointer(fmt.Sprintf("absent-%d", i)),
						}
					}
					matches := make([]int, 0, 32)
					b.ReportAllocs()
					b.ResetTimer()
					for i := range b.N {
						matches = matches[:0]
						if path != "Index" {
							query := queries[i%len(queries)]
							if path == "LocalStable" {
								query = queries[0]
							}
							searcher.Search(query, &matches)
						} else {
							index.Search(queries[i%len(queries)], &matches)
						}
					}
					b.StopTimer()
					if enabled && path != "LocalStable" {
						b.ReportMetric(1, "antonym-merges/op")
					}
				})
			}
		}
	}
}
