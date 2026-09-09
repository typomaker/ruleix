//nolint:lll // Migration benchmarks keep legacy pointer getters inline.
package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/typomaker/ruleix"
)

const (
	benchmarkEntries     = 10_000
	benchmarkCardinality = 100
)

var (
	benchmarkStringResult []string
	benchmarkIntResult    []int
	benchmarkLocalResult  *ruleix.Local[benchmarkEquality, int]
	benchmarkBitmapResult *roaring.Bitmap
	benchmarkBitmapStats  roaring.Statistics
	benchmarkBytesResult  []byte
	benchmarkBoolResult   bool
	benchmarkUint64Result uint64
)

//nolint:gocognit // Benchmark matrix intentionally compares every result shape and traversal mode.
func BenchmarkBitmapResultIteration(b *testing.B) {
	// Iterate avoids the iterator allocation, while ManyIterator amortizes calls
	// for wide results. Compare both result-materialization shapes.
	for _, cardinality := range []uint64{16, 4 << 10, 100_000} {
		for _, shape := range []string{"Dense", "Sparse"} {
			bits := roaring.New()
			step := uint64(1)
			if shape == "Sparse" {
				step = 97
			}
			for id := uint64(0); id < cardinality*step; id += step {
				bits.Add(uint32(id))
			}
			name := fmt.Sprintf("%s/%d", shape, cardinality)
			b.Run(name+"/Full/Iterator", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var sum uint64
					iterator := bits.Iterator()
					for iterator.HasNext() {
						sum += uint64(iterator.Next())
					}
					benchmarkUint64Result = sum
				}
			})
			b.Run(name+"/Full/ManyIterator", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var sum uint64
					iterator := bits.ManyIterator()
					var values [256]uint32
					for count := iterator.NextMany(values[:]); count != 0; count = iterator.NextMany(values[:]) {
						for _, value := range values[:count] {
							sum += uint64(value)
						}
					}
					benchmarkUint64Result = sum
				}
			})
			b.Run(name+"/Full/Iterate", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var sum uint64
					bits.Iterate(func(value uint32) bool {
						sum += uint64(value)
						return true
					})
					benchmarkUint64Result = sum
				}
			})
			b.Run(name+"/First16/Iterator", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var sum uint64
					iterator := bits.Iterator()
					for count := 0; count < 16 && iterator.HasNext(); count++ {
						sum += uint64(iterator.Next())
					}
					benchmarkUint64Result = sum
				}
			})
			b.Run(name+"/First16/Iterate", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var sum uint64
					count := 0
					bits.Iterate(func(value uint32) bool {
						sum += uint64(value)
						count++
						return count < 16
					})
					benchmarkUint64Result = sum
				}
			})
		}
	}
}

func BenchmarkBitmapBoundaries(b *testing.B) {
	bits := roaring.New()
	for id := uint32(1); id < 10_000_000; id += 97 {
		bits.Add(id)
	}
	target := uint32(5_000_000)

	for _, benchmark := range []struct {
		name string
		call func() uint64
	}{
		{name: "Minimum", call: func() uint64 { return uint64(bits.Minimum()) }},
		{name: "Maximum", call: func() uint64 { return uint64(bits.Maximum()) }},
		{name: "NextValue", call: func() uint64 { return uint64(bits.NextValue(target)) }},
		{name: "PreviousValue", call: func() uint64 { return uint64(bits.PreviousValue(target)) }},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = benchmark.call()
			}
		})
	}
}

//nolint:gocognit // Benchmark matrix intentionally compares pagination primitives across result shapes.
func BenchmarkBitmapPagination(b *testing.B) {
	const (
		cardinality = uint32(100_000)
		pageSize    = uint32(16)
	)
	for _, shape := range []string{"Dense", "Sparse"} {
		bits := roaring.New()
		step := uint32(1)
		if shape == "Sparse" {
			step = 97
		}
		for id := uint32(0); id < cardinality; id++ {
			bits.Add(id * step)
		}
		for _, offset := range []uint32{16, 4 << 10, 64 << 10} {
			prefix := fmt.Sprintf("%s/Offset%d/", shape, offset)
			b.Run(prefix+"WalkPage", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					iterator := bits.Iterator()
					for range offset {
						iterator.Next()
					}
					var sum uint64
					for range pageSize {
						sum += uint64(iterator.Next())
					}
					benchmarkUint64Result = sum
				}
			})
			b.Run(prefix+"SelectAndAdvancePage", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					first, err := bits.Select(offset)
					if err != nil {
						b.Fatal(err)
					}
					iterator := bits.Iterator()
					iterator.AdvanceIfNeeded(first)
					var sum uint64
					for range pageSize {
						sum += uint64(iterator.Next())
					}
					benchmarkUint64Result = sum
				}
			})
			cursor := offset * step
			b.Run(prefix+"WalkRank", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var rank uint64
					iterator := bits.Iterator()
					for iterator.HasNext() && iterator.PeekNext() <= cursor {
						iterator.Next()
						rank++
					}
					benchmarkUint64Result = rank
				}
			})
			b.Run(prefix+"Rank", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					benchmarkUint64Result = bits.Rank(cursor)
				}
			})
		}
	}
}

func BenchmarkBitmapAndAny(b *testing.B) {
	const postingsCount = 64
	candidates := roaring.New()
	candidates.AddRange(0, 100_000)
	postings := make([]*roaring.Bitmap, postingsCount)
	for posting := range postings {
		bits := roaring.New()
		for id := uint32(posting); id < 100_000; id += postingsCount * 2 {
			bits.Add(id)
		}
		postings[posting] = bits
	}
	b.Run("UnionThenAnd", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			union := roaring.New()
			for _, posting := range postings {
				union.Or(posting)
			}
			result := candidates.Clone()
			result.And(union)
			benchmarkUint64Result = result.GetCardinality()
		}
	})
	b.Run("AndAny", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := candidates.Clone()
			result.AndAny(postings...)
			benchmarkUint64Result = result.GetCardinality()
		}
	})
}

//nolint:gocognit // Benchmark matrix intentionally keeps all union strategies together.
func BenchmarkBitmapOrStrategies(b *testing.B) {
	// HeapOr's heap and intermediate bitmap overhead does not pay off for either
	// evenly sized or strongly skewed posting lists in the index's size range.
	// ParOr can outperform FastOr for many similarly sized sparse postings, but
	// is several times slower for skewed or heavily overlapping postings. Keep
	// it out of search until a planner can distinguish those shapes cheaply.
	for _, shape := range []string{"Uniform", "Skewed"} {
		for _, postingsCount := range []int{4, 16, 64, 256} {
			postings := make([]*roaring.Bitmap, postingsCount)
			for posting := range postings {
				bits := roaring.New()
				if shape == "Uniform" {
					for id := uint32(posting); id < 100_000; id += uint32(postingsCount) {
						bits.Add(id)
					}
				} else {
					bits.AddRange(0, uint64(100_000/(posting+1)))
				}
				postings[posting] = bits
			}
			prefix := fmt.Sprintf("%s/Postings%d/", shape, postingsCount)
			b.Run(prefix+"Sequential", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					result := roaring.New()
					for _, posting := range postings {
						result.Or(posting)
					}
					benchmarkUint64Result = result.GetCardinality()
				}
			})
			b.Run(prefix+"FastOr", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					result := roaring.FastOr(postings...)
					benchmarkUint64Result = result.GetCardinality()
				}
			})
			b.Run(prefix+"HeapOr", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					result := roaring.HeapOr(postings...)
					benchmarkUint64Result = result.GetCardinality()
				}
			})
			if postingsCount >= 64 {
				for _, parallelism := range []int{2, 4, 8} {
					b.Run(fmt.Sprintf("%sParOr%d", prefix, parallelism), func(b *testing.B) {
						b.ReportAllocs()
						for range b.N {
							result := roaring.ParOr(parallelism, postings...)
							benchmarkUint64Result = result.GetCardinality()
						}
					})
				}
			}
		}
	}
}

//nolint:gocognit // Benchmark matrix intentionally keeps all intersection strategies together.
func BenchmarkBitmapFastAnd(b *testing.B) {
	// ParAnd parallelizes work by high-key containers. It does not amortize its
	// goroutine, heap, and merge overhead in the normal range or even across 10M
	// IDs, so keep FastAnd in the search path.
	for _, postingsCount := range []int{2, 4, 8, 16} {
		postings := make([]*roaring.Bitmap, postingsCount)
		for posting := range postings {
			bits := roaring.New()
			bits.AddRange(uint64(posting*100), uint64(100_000-posting*100))
			postings[posting] = bits
		}
		b.Run(fmt.Sprintf("Postings%d/Sequential", postingsCount), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := postings[0].Clone()
				for _, posting := range postings[1:] {
					result.And(posting)
				}
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		b.Run(fmt.Sprintf("Postings%d/FastAnd", postingsCount), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := roaring.FastAnd(postings...)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		for _, parallelism := range []int{2, 4} {
			b.Run(fmt.Sprintf("Postings%d/ParAnd%d", postingsCount, parallelism), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					result := roaring.ParAnd(parallelism, postings...)
					benchmarkUint64Result = result.GetCardinality()
				}
			})
		}
	}

	large := make([]*roaring.Bitmap, 8)
	for posting := range large {
		large[posting] = roaring.New()
		large[posting].AddRange(uint64(posting*100), uint64(10_000_000-posting*100))
	}
	b.Run("LargeContainers/FastAnd", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := roaring.FastAnd(large...)
			benchmarkUint64Result = result.GetCardinality()
		}
	})
	for _, parallelism := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("LargeContainers/ParAnd%d", parallelism), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := roaring.ParAnd(parallelism, large...)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
	}

	disjoint := []*roaring.Bitmap{roaring.New(), roaring.New(), roaring.New(), roaring.New()}
	disjoint[0].AddRange(0, 25_000)
	disjoint[1].AddRange(25_000, 50_000)
	disjoint[2].AddRange(0, 100_000)
	disjoint[3].AddRange(0, 100_000)
	b.Run("EarlyEmpty/Sequential", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := disjoint[0].Clone()
			for _, posting := range disjoint[1:] {
				if result.IsEmpty() {
					break
				}
				result.And(posting)
			}
			benchmarkUint64Result = result.GetCardinality()
		}
	})
	b.Run("EarlyEmpty/FastAnd", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := roaring.FastAnd(disjoint...)
			benchmarkUint64Result = result.GetCardinality()
		}
	})
}

func BenchmarkBitmapOrCardinality(b *testing.B) {
	for _, overlapPercent := range []int{0, 50, 100} {
		left := roaring.New()
		left.AddRange(0, 100_000)
		right := roaring.New()
		overlap := uint64(overlapPercent * 1_000)
		right.AddRange(100_000-overlap, 200_000-overlap)

		b.Run(fmt.Sprintf("Overlap%d/MaterializeUnion", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := left.Clone()
				result.Or(right)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		b.Run(fmt.Sprintf("Overlap%d/OrCardinality", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = left.OrCardinality(right)
			}
		})
		b.Run(fmt.Sprintf("Overlap%d/OrCardinalityThenUnion", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = left.OrCardinality(right)
				result := left.Clone()
				result.Or(right)
				benchmarkUint64Result += result.GetCardinality()
			}
		})
	}
}

func BenchmarkBitmapRunOptimize(b *testing.B) {
	// Ruleix materializes unions and intersections during Search. This workload
	// captures why build-time RunOptimize is not enabled globally: it reduces
	// retained bitmap bytes, but operations on these fragmented runs cost more.
	left := roaring.New()
	right := roaring.New()
	for start := uint32(0); start < 100_000; start += 200 {
		for id := start; id < start+150; id++ {
			left.Add(id)
		}
		for id := start + 50; id < start+200; id++ {
			right.Add(id)
		}
	}
	optimizedLeft := left.Clone()
	optimizedRight := right.Clone()
	optimizedLeft.RunOptimize()
	optimizedRight.RunOptimize()

	for _, operation := range []struct {
		name  string
		apply func(*roaring.Bitmap, *roaring.Bitmap)
	}{
		{name: "Or", apply: (*roaring.Bitmap).Or},
		{name: "And", apply: (*roaring.Bitmap).And},
	} {
		b.Run(operation.name+"/Regular", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := left.Clone()
				operation.apply(result, right)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		b.Run(operation.name+"/RunOptimized", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := optimizedLeft.Clone()
				operation.apply(result, optimizedRight)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
	}
}
