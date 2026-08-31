package ruleix

import (
	"fmt"
	"runtime"
	"testing"
)

var canonicalBenchmarkMatches []int
var canonicalBenchmarkIndex *Index[canonicalConstraint, int]

func canonicalAliasSchema(aliases int, shared bool) Rule[canonicalConstraint] {
	getter := func(v canonicalConstraint) (int, bool) { return v.value, true }
	children := make([]Rule[canonicalConstraint], aliases)
	if shared {
		rule := Include(getter)
		for i := range children {
			children[i] = rule
		}
	} else {
		for i := range children {
			children[i] = Include(getter)
		}
	}
	return All(children...)
}

var canonicalBenchmarkIndexes []*Index[canonicalConstraint, int]

// BenchmarkCanonicalAllRetainedMemory last local run (Apple M1 Max, Go 1.26.0):
// go test -run '^$' -bench '^BenchmarkCanonicalAllRetainedMemory/' -benchtime=3x -count=3 .
// Eight aliases retained 65,664 B/index and 1,549 B/warm-Local when shared,
// versus 107,931 B/index and 4,421 B/warm-Local when independent.
//
//nolint:gocognit // Keeping the benchmark matrix together makes its cases comparable.
func BenchmarkCanonicalAllRetainedMemory(b *testing.B) {
	constraints, ids := canonicalBenchmarkData()
	query := canonicalConstraint{value: 17}
	for _, aliases := range []int{2, 4, 8} {
		for _, shared := range []bool{true, false} {
			kind := "Independent"
			if shared {
				kind = "Shared"
			}
			prefix := fmt.Sprintf("%s/%d", kind, aliases)
			b.Run(prefix+"/Index", func(b *testing.B) {
				runtime.GC()
				runtime.GC()
				before := canonicalHeapAlloc()
				indexes := make([]*Index[canonicalConstraint, int], b.N)
				for i := range b.N {
					index, err := New[canonicalConstraint, int](canonicalAliasSchema(aliases, shared)).Build(Zip(constraints, ids))
					if err != nil {
						b.Fatal(err)
					}
					indexes[i] = index
				}
				runtime.GC()
				runtime.GC()
				after := canonicalHeapAlloc()
				runtime.KeepAlive(indexes)
				if after > before {
					b.ReportMetric(float64(after-before)/float64(b.N), "retained-B/index")
				}
				canonicalBenchmarkIndexes = indexes
			})
			b.Run(prefix+"/WarmLocal", func(b *testing.B) {
				index, err := New[canonicalConstraint, int](canonicalAliasSchema(aliases, shared)).Build(Zip(constraints, ids))
				if err != nil {
					b.Fatal(err)
				}
				matches := make([]int, 0, 64)
				runtime.GC()
				runtime.GC()
				before := canonicalHeapAlloc()
				locals := make([]*Local[canonicalConstraint, int], b.N)
				for i := range b.N {
					local := index.Local()
					for range 3 {
						matches = matches[:0]
						local.Search(query, &matches)
					}
					locals[i] = local
				}
				runtime.GC()
				runtime.GC()
				after := canonicalHeapAlloc()
				runtime.KeepAlive(index)
				runtime.KeepAlive(locals)
				if after > before {
					b.ReportMetric(float64(after-before)/float64(b.N), "retained-B/local")
				}
				for _, local := range locals {
					local.Close()
				}
			})
		}
	}
}

func canonicalHeapAlloc() uint64 {
	var statistics runtime.MemStats
	runtime.ReadMemStats(&statistics)
	return statistics.HeapAlloc
}

func canonicalBenchmarkData() ([]canonicalConstraint, []int) {
	constraints := make([]canonicalConstraint, 4096)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = canonicalConstraint{value: i % 64, until: i + 1, operator: OperatorEQ}
		ids[i] = i
	}
	return constraints, ids
}

// BenchmarkCanonicalAllAliases last local run (Apple M1 Max, Go 1.26.0):
// go test -run '^$' -bench '^BenchmarkCanonicalAllAliases/' -benchmem -benchtime=500ms -count=5 .
// Shared/8: Index 215.9 ns/op, 0 B/op, 0 allocs/op; WarmLocal 203.2 ns/op,
// 0 B/op, 0 allocs/op. Independent/8: Index 1,880 ns/op, 152 B/op,
// 2 allocs/op; WarmLocal 384.0 ns/op, 0 B/op, 0 allocs/op.
func BenchmarkCanonicalAllAliases(b *testing.B) {
	constraints, ids := canonicalBenchmarkData()
	query := canonicalConstraint{value: 17}
	for _, aliases := range []int{2, 4, 8} {
		for _, shared := range []bool{true, false} {
			kind := "Independent"
			if shared {
				kind = "Shared"
			}
			index, err := New[canonicalConstraint, int](canonicalAliasSchema(aliases, shared)).Build(Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			prefix := fmt.Sprintf("%s/%d", kind, aliases)
			b.Run(prefix+"/Index", func(b *testing.B) {
				matches := make([]int, 0, 64)
				b.ReportAllocs()
				for range b.N {
					matches = matches[:0]
					index.Search(query, &matches)
				}
				canonicalBenchmarkMatches = matches
			})
			b.Run(prefix+"/WarmLocal", func(b *testing.B) {
				local := index.Local()
				defer local.Close()
				matches := make([]int, 0, 64)
				for range 3 {
					local.Search(query, &matches)
					matches = matches[:0]
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					matches = matches[:0]
					local.Search(query, &matches)
				}
				canonicalBenchmarkMatches = matches
			})
		}
	}
}

// BenchmarkCanonicalAllBuild last local run (Apple M1 Max, Go 1.26.0):
// go test -run '^$' -bench '^BenchmarkCanonicalAllBuild/' -benchmem -benchtime=500ms -count=5 .
// Shared/8: 237.6 us/op, 257,282 B/op, 1,528 allocs/op; Independent/8:
// 1.217 ms/op, 681,980 B/op, 11,945 allocs/op.
func BenchmarkCanonicalAllBuild(b *testing.B) {
	constraints, ids := canonicalBenchmarkData()
	for _, aliases := range []int{2, 4, 8} {
		for _, shared := range []bool{true, false} {
			kind := "Independent"
			if shared {
				kind = "Shared"
			}
			b.Run(fmt.Sprintf("%s/%d", kind, aliases), func(b *testing.B) {
				builder := New[canonicalConstraint, int](canonicalAliasSchema(aliases, shared))
				b.ReportAllocs()
				for range b.N {
					index, err := builder.Build(Zip(constraints, ids))
					if err != nil {
						b.Fatal(err)
					}
					canonicalBenchmarkIndex = index
				}
			})
		}
	}
}
