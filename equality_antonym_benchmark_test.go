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

func buildAntonymGraphBenchmarkIndex(b *testing.B, component bool) *Index[antonymGraphConstraint, int] {
	b.Helper()
	constraints := make([]antonymGraphConstraint, 4096)
	ids := make([]int, len(constraints))
	for id := range constraints {
		ids[id] = id
		from, until := 0, 3
		if id < len(constraints)/2 {
			from, until = 3, 6
		}
		for field := from; field < until; field++ {
			constraints[id].values[field] = antonymPointer(id % 128)
		}
	}
	index, _, err := buildIndexPhysicalAliases(
		antonymGraphSchema(6), Zip(constraints, ids), false, nil,
		buildOptions{compilePhysicalAliases: true, compileStrictAntonyms: component, enableStreaming: true},
	)
	if err != nil {
		b.Fatal(err)
	}
	if !component {
		index.root = compileStrictEqualityAntonymPairsForBenchmark(index.root)
		prepareRuleSearch(index.root)
	}
	return index
}

func compileStrictEqualityAntonymPairsForBenchmark[T any](rule Rule[T]) Rule[T] {
	all := rule.(*allRule[T])
	consumed := make([]bool, len(all.children))
	var children []Rule[T]
	for first, child := range all.children {
		if consumed[first] {
			continue
		}
		left := strictEqualityOperandOf(child)
		for second := first + 1; second < len(all.children); second++ {
			right := strictEqualityOperandOf(all.children[second])
			if consumed[second] || right == nil || !strictWildcardComplements(left, right) {
				continue
			}
			leftClass := strictEqualityAntonymClass[T]{first: first, members: []int{first}, operands: []strictEqualityOperand[T]{left}}
			rightClass := strictEqualityAntonymClass[T]{first: second, members: []int{second}, operands: []strictEqualityOperand[T]{right}}
			children = append(children, newStrictEqualityAntonymRule(all.children, leftClass, rightClass))
			consumed[first], consumed[second] = true, true
			break
		}
		if !consumed[first] {
			children = append(children, child)
		}
	}
	all.children = children
	all.execution = nil
	all.queryKeyProviders = nil
	all.planningProviders = nil
	all.sharedWildcardGroups = nil
	all.duplicateEquality = nil
	all.planningPrepared = false
	return all
}

// BenchmarkStrictEqualityAntonymGraph compares the f0b0d68 one-partner shape
// with whole 3x3 complement components. On an M1 Max with Go 1.26.0,
// GOMAXPROCS=1, 1s x5, Index medians were 4,125/2,239 ns and 19/5 allocs;
// rotating Local 2,758/2,467 ns and 23/11 allocs; stable Local 143.5/143.2 ns
// and 0/0 allocs. Full command and retained results are in the design document.
func BenchmarkStrictEqualityAntonymGraph(b *testing.B) {
	for _, component := range []bool{false, true} {
		variant := "Pairs"
		if component {
			variant = "Component"
		}
		for _, path := range []string{"Index", "LocalRotating", "LocalStable"} {
			b.Run(variant+"/"+path, func(b *testing.B) {
				index := buildAntonymGraphBenchmarkIndex(b, component)
				local := index.Local()
				b.Cleanup(local.Close)
				queries := make([]antonymGraphConstraint, 8)
				for query := range queries {
					for field := range 6 {
						queries[query].values[field] = antonymPointer(query)
					}
				}
				var matches []int
				b.ReportAllocs()
				b.ResetTimer()
				for i := range b.N {
					matches = matches[:0]
					query := queries[i%len(queries)]
					if path == "LocalStable" {
						query = queries[0]
					}
					if path == "Index" {
						index.Search(query, &matches)
					} else {
						local.Search(query, &matches)
					}
				}
				b.StopTimer()
				operands := 3.0
				if component {
					operands = 1
				}
				b.ReportMetric(operands, "compiled-operands/op")
			})
		}
	}
}

// BenchmarkStrictEqualityAntonymGraphRetainedMemory compares warmed Local
// state for three pair operands and one equivalent 3x3 component.
func BenchmarkStrictEqualityAntonymGraphRetainedMemory(b *testing.B) {
	for _, component := range []bool{false, true} {
		variant := "Pairs"
		if component {
			variant = "Component"
		}
		b.Run(variant, func(b *testing.B) {
			index := buildAntonymGraphBenchmarkIndex(b, component)
			locals := make([]*Local[antonymGraphConstraint, int], b.N)
			query := antonymGraphConstraint{}
			for field := range 6 {
				query.values[field] = antonymPointer(1)
			}
			var matches []int
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

func buildAntonymSelectivityBenchmarkIndex(
	b *testing.B, modulus [4]int,
) *Index[antonymGraphConstraint, int] {
	b.Helper()
	const entries = 8192
	constraints := make([]antonymGraphConstraint, entries)
	ids := make([]int, entries)
	for id := range constraints {
		ids[id] = id
		from, until := 0, 4
		if id < entries/2 {
			from, until = 4, 8
		}
		for field := from; field < until; field++ {
			constraints[id].values[field] = antonymPointer(id % modulus[field%4])
		}
	}
	index, _, err := buildIndexPhysicalAliases(
		antonymGraphSchema(8), Zip(constraints, ids), false, nil,
		buildOptions{compilePhysicalAliases: true, compileStrictAntonyms: true, enableStreaming: true},
	)
	if err != nil {
		b.Fatal(err)
	}
	return index
}

// BenchmarkStrictEqualityAntonymSelectivityOrder exercises bitmap-sized
// postings whose schema order is 2,048, 1,024, 586, and 585 IDs per side.
// M1 Max, Go 1.26.0, GOMAXPROCS=1: 8s CPU runs kept general Index neutral at
// 11,185/11,200 ns; 5s EarlyEmpty improved 10,074/8,244 ns. Bytes and
// allocation counts stayed 8,905/6 and 8,240/4. See the design document.
func BenchmarkStrictEqualityAntonymSelectivityOrder(b *testing.B) {
	for _, path := range []string{"Index", "LocalRotating", "LocalStable", "EarlyEmpty"} {
		b.Run(path, func(b *testing.B) {
			index := buildAntonymSelectivityBenchmarkIndex(b, [4]int{2, 4, 7, 7})
			local := index.Local()
			b.Cleanup(local.Close)
			queries := make([]antonymGraphConstraint, 8)
			for query := range queries {
				for field := range 8 {
					queries[query].values[field] = antonymPointer(query % 2)
				}
			}
			if path == "EarlyEmpty" {
				for field := 0; field < 8; field += 4 {
					queries[0].values[field+2] = antonymPointer(0)
					queries[0].values[field+3] = antonymPointer(1)
				}
			}
			var matches []int
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				matches = matches[:0]
				query := queries[i%len(queries)]
				if path == "LocalStable" || path == "EarlyEmpty" {
					query = queries[0]
				}
				if path == "Index" || path == "EarlyEmpty" {
					index.Search(query, &matches)
				} else {
					local.Search(query, &matches)
				}
			}
		})
	}
}

// BenchmarkStrictEqualityAntonymUniformOrder is the equal-cardinality guard:
// choosing an order cannot reduce intermediate bitmap sizes in this fixture.
func BenchmarkStrictEqualityAntonymUniformOrder(b *testing.B) {
	index := buildAntonymSelectivityBenchmarkIndex(b, [4]int{2, 2, 2, 2})
	query := antonymGraphConstraint{}
	for field := range 8 {
		query.values[field] = antonymPointer(0)
	}
	var matches []int
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		matches = matches[:0]
		index.Search(query, &matches)
	}
}
