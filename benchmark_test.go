//nolint:lll // Migration benchmarks keep legacy pointer getters inline.
package ruleix_test

import (
	"cmp"
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
	// Iterate avoids the iterator allocation and supports early termination,
	// while ManyIterator amortizes calls for wide results. Compare both shapes
	// used by result materialization and by Visit's early-limit behavior.
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

func BenchmarkBitmapIntersects(b *testing.B) {
	// Intersects is valuable when an empty result avoids later materialization,
	// but it duplicates the intersection pass when the result is still needed.
	for _, tt := range []struct {
		name       string
		rightStart uint64
	}{
		{name: "Disjoint", rightStart: 100_000},
		{name: "LateMatch", rightStart: 99_999},
		{name: "HalfOverlap", rightStart: 50_000},
	} {
		left := roaring.New()
		left.AddRange(0, 100_000)
		right := roaring.New()
		right.AddRange(tt.rightStart, tt.rightStart+100_000)

		b.Run(tt.name+"/AndIsEmpty", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := left.Clone()
				result.And(right)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		b.Run(tt.name+"/Intersects", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if left.Intersects(right) {
					benchmarkUint64Result = 1
				} else {
					benchmarkUint64Result = 0
				}
			}
		})
		b.Run(tt.name+"/IntersectsThenAnd", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if !left.Intersects(right) {
					benchmarkUint64Result = 0
					continue
				}
				result := left.Clone()
				result.And(right)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
	}
}

//nolint:gocognit // Benchmark matrix intentionally compares every shape and interval case.
func BenchmarkBitmapIntersectsWithInterval(b *testing.B) {
	// Ruleix's ordered filters range over stored values, while this operation
	// ranges over internal row IDs. It therefore only applies to a future ID
	// range/pagination API, where it can avoid materializing an interval bitmap.
	for _, shape := range []string{"Dense", "Sparse"} {
		bits := roaring.New()
		if shape == "Dense" {
			bits.AddRange(0, 100_000)
		} else {
			for id := uint32(0); id < 100_000; id += 100 {
				bits.Add(id)
			}
		}
		for _, interval := range []struct {
			name  string
			start uint64
			end   uint64
		}{
			{name: "EarlyHit", start: 0, end: 100},
			{name: "LateHit", start: 99_900, end: 100_000},
			{name: "Miss", start: 100_000, end: 110_000},
		} {
			intervalBits := roaring.New()
			intervalBits.AddRange(interval.start, interval.end)
			prefix := shape + "/" + interval.name + "/"
			b.Run(prefix+"IntersectsWithInterval", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if bits.IntersectsWithInterval(interval.start, interval.end) {
						benchmarkUint64Result = 1
					} else {
						benchmarkUint64Result = 0
					}
				}
			})
			b.Run(prefix+"PrebuiltIntervalBitmap", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if bits.Intersects(intervalBits) {
						benchmarkUint64Result = 1
					} else {
						benchmarkUint64Result = 0
					}
				}
			})
			b.Run(prefix+"BuildIntervalBitmap", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					query := roaring.New()
					query.AddRange(interval.start, interval.end)
					if bits.Intersects(query) {
						benchmarkUint64Result = 1
					} else {
						benchmarkUint64Result = 0
					}
				}
			})
		}
	}
}

func BenchmarkBitmapAndCardinality(b *testing.B) {
	// The cardinality-only operation is useful to a planner or emptiness check;
	// doing it before an intersection that is still needed duplicates work.
	for _, overlapPercent := range []int{0, 1, 50, 100} {
		left := roaring.New()
		left.AddRange(0, 100_000)
		right := roaring.New()
		overlap := uint64(overlapPercent * 1_000)
		right.AddRange(100_000-overlap, 200_000-overlap)

		b.Run(fmt.Sprintf("Overlap%d/MaterializeIntersection", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result := left.Clone()
				result.And(right)
				benchmarkUint64Result = result.GetCardinality()
			}
		})
		b.Run(fmt.Sprintf("Overlap%d/AndCardinality", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = left.AndCardinality(right)
			}
		})
		b.Run(fmt.Sprintf("Overlap%d/AndCardinalityThenIntersection", overlapPercent), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = left.AndCardinality(right)
				result := left.Clone()
				result.And(right)
				benchmarkUint64Result += result.GetCardinality()
			}
		})
	}
}

func BenchmarkBitmapAddMany(b *testing.B) {
	// AddMany can benefit a future bulk builder. The current builder streams one
	// ID into many postings, so using it would require per-posting buffers and a
	// separate memory tradeoff; small equality batches are benchmarked elsewhere.
	const count = 100_000
	sequential := make([]uint32, count)
	sparse := make([]uint32, count)
	shuffled := make([]uint32, count)
	for i := range count {
		sequential[i] = uint32(i)
		sparse[i] = uint32(i * 16)
		shuffled[i] = uint32((i * 65_537) % count)
	}

	for _, tt := range []struct {
		name   string
		values []uint32
	}{
		{name: "Sequential", values: sequential},
		{name: "Sparse", values: sparse},
		{name: "Shuffled", values: shuffled},
	} {
		b.Run(tt.name+"/Add", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := roaring.New()
				for _, value := range tt.values {
					bits.Add(value)
				}
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
		b.Run(tt.name+"/AddMany", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := roaring.New()
				bits.AddMany(tt.values)
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
	}
}

//nolint:gocognit // Benchmark matrix intentionally compares each range primitive with its scalar equivalent.
func BenchmarkBitmapRangeMutations(b *testing.B) {
	for _, size := range []uint64{1_000, 100_000} {
		name := fmt.Sprintf("Size%d/", size)
		b.Run(name+"AddLoop", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := roaring.New()
				for id := uint64(0); id < size; id++ {
					bits.Add(uint32(id))
				}
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
		b.Run(name+"AddRange", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := roaring.New()
				bits.AddRange(0, size)
				benchmarkUint64Result = bits.GetCardinality()
			}
		})

		dense := roaring.New()
		dense.AddRange(0, size*2)
		b.Run(name+"RemoveLoop", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := dense.Clone()
				for id := uint64(0); id < size; id++ {
					bits.Remove(uint32(id))
				}
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
		b.Run(name+"RemoveRange", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := dense.Clone()
				bits.RemoveRange(0, size)
				benchmarkUint64Result = bits.GetCardinality()
			}
		})

		alternating := roaring.New()
		for id := uint64(0); id < size*2; id += 2 {
			alternating.Add(uint32(id))
		}
		mask := roaring.New()
		mask.AddRange(0, size)
		b.Run(name+"XorRangeBitmap", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := alternating.Clone()
				bits.Xor(mask)
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
		b.Run(name+"Flip", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := alternating.Clone()
				bits.Flip(0, size)
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
	}
}

func BenchmarkBitmapCheckedAdd(b *testing.B) {
	// The streaming builder does not use the inserted/not-inserted result, so
	// CheckedAdd must outperform Add by itself to justify replacing it. Include
	// repeated IDs because one external ID can occur in multiple stored rules.
	const count = 100_000
	sequential := make([]uint32, count)
	sparse := make([]uint32, count)
	shuffled := make([]uint32, count)
	repeated := make([]uint32, count)
	for i := range count {
		sequential[i] = uint32(i)
		sparse[i] = uint32(i * 16)
		shuffled[i] = uint32((i * 65_537) % count)
		repeated[i] = uint32(i / 4)
	}

	for _, tt := range []struct {
		name   string
		values []uint32
	}{
		{name: "Sequential", values: sequential},
		{name: "Sparse", values: sparse},
		{name: "Shuffled", values: shuffled},
		{name: "Repeated", values: repeated},
	} {
		b.Run(tt.name+"/Add", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := roaring.New()
				for _, value := range tt.values {
					bits.Add(value)
				}
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
		b.Run(tt.name+"/CheckedAdd", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				bits := roaring.New()
				for _, value := range tt.values {
					bits.CheckedAdd(value)
				}
				benchmarkUint64Result = bits.GetCardinality()
			}
		})
	}
}

func BenchmarkBitmapPlannerSignals(b *testing.B) {
	// These APIs expose potentially useful shape information, but a planner
	// would inspect postings on every search. Measure the signal itself rather
	// than folding its cost into an operation whose strategy is not yet chosen.
	shapes := []struct {
		name string
		bits *roaring.Bitmap
	}{
		{name: "Empty", bits: roaring.New()},
		{name: "Dense", bits: roaring.New()},
		{name: "Sparse", bits: roaring.New()},
		{name: "ManyContainers", bits: roaring.New()},
		{name: "RunCompressed", bits: roaring.New()},
	}
	shapes[1].bits.AddRange(0, 100_000)
	for id := uint32(0); id < 100_000; id += 100 {
		shapes[2].bits.Add(id)
	}
	for id := uint32(0); id < 10_000_000; id += 65_537 {
		shapes[3].bits.Add(id)
	}
	for start := uint64(0); start < 100_000; start += 200 {
		shapes[4].bits.AddRange(start, start+150)
	}
	shapes[4].bits.RunOptimize()

	for _, shape := range shapes {
		b.Run(shape.name+"/Cardinality", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = shape.bits.GetCardinality()
			}
		})
		b.Run(shape.name+"/DenseSize", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkUint64Result = shape.bits.DenseSize()
			}
		})
		b.Run(shape.name+"/HasRunCompression", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkBoolResult = shape.bits.HasRunCompression()
			}
		})
		b.Run(shape.name+"/Stats", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkBitmapStats = shape.bits.Stats()
			}
		})
		b.Run(shape.name+"/AllSignals", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkBitmapStats = shape.bits.Stats()
				benchmarkUint64Result = shape.bits.DenseSize()
				benchmarkBoolResult = shape.bits.HasRunCompression()
			}
		})
	}
}

// BenchmarkBitmapFrozenView evaluates Roaring's persistent/memory-mapped
// representation independently of an index persistence API. A frozen view
// keeps its serialized buffer alive and allocates bitmap metadata when opened;
// the current in-memory builder has neither a shared backing file nor an index
// format over which those fixed costs could be amortized.
func BenchmarkBitmapFrozenView(b *testing.B) {
	for _, shape := range []struct {
		name string
		make func() *roaring.Bitmap
	}{
		{name: "Dense", make: func() *roaring.Bitmap {
			bits := roaring.New()
			bits.AddRange(0, 100_000)
			return bits
		}},
		{name: "Sparse", make: func() *roaring.Bitmap {
			bits := roaring.New()
			for id := uint32(0); id < 10_000_000; id += 97 {
				bits.Add(id)
			}
			return bits
		}},
	} {
		b.Run(shape.name, func(b *testing.B) { benchmarkBitmapFrozenShape(b, shape.make()) })
	}
}

func benchmarkBitmapFrozenShape(b *testing.B, original *roaring.Bitmap) {
	frozenBuffer, err := original.Freeze()
	if err != nil {
		b.Fatal(err)
	}
	frozen := roaring.New()
	if err := frozen.FrozenView(frozenBuffer); err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(original.GetSizeInBytes()), "original-bytes")
	b.ReportMetric(float64(len(frozenBuffer)), "frozen-bytes")

	b.Run("Freeze", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkBytesResult, err = original.Freeze()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Open", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(original.GetSizeInBytes()), "original-bytes")
		b.ReportMetric(float64(len(frozenBuffer)), "frozen-bytes")
		for range b.N {
			view := roaring.New()
			if err := view.FrozenView(frozenBuffer); err != nil {
				b.Fatal(err)
			}
			benchmarkBitmapResult = view
		}
	})

	probe := roaring.New()
	probe.AddRange(50_000, 150_000)
	benchmarkBitmapFrozenOperations(b, probe, "Original", original)
	benchmarkBitmapFrozenOperations(b, probe, "Frozen", frozen)
}

func benchmarkBitmapFrozenOperations(b *testing.B, probe *roaring.Bitmap, name string, bits *roaring.Bitmap) {
	b.Run(name+"/Or", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := probe.Clone()
			result.Or(bits)
			benchmarkBitmapResult = result
		}
	})
	b.Run(name+"/And", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := probe.Clone()
			result.And(bits)
			benchmarkBitmapResult = result
		}
	})
	b.Run(name+"/Iterate", func(b *testing.B) {
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
}

type benchmarkEquality struct {
	optional *int
	required int
}
type benchmarkRange struct {
	operator *ruleix.Operator
	value    *int
}
type benchmarkInterval struct{ from, until *int }
type benchmarkAllValue struct{ a, b, c, d *int }
type benchmarkExcludeValue struct{ include, excludeA, excludeB *int }
type benchmarkCardinalityOrderValue struct{ threshold, group *int }

type benchmarkSkewedEqualityValue struct{ manyValues, skewed *int }

func benchmarkPtr(value int) *int { return &value }

func buildGenerated[C any, ID comparable](
	b *testing.B,
	schema ruleix.Rule[C],
	count int,
	entry func(int) (C, ID),
) *ruleix.Index[C, ID] {
	b.Helper()
	ix, err := ruleix.New[C, ID](schema).Build(func(yield func(C, ID) bool) {
		for n := 0; n < count; n++ {
			value, id := entry(n)
			if !yield(value, id) {
				return
			}
		}
	})
	if err != nil {
		b.Fatal(err)
	}
	return ix
}

func benchmarkEqIndex(b *testing.B, highCardinality bool) *ruleix.Index[benchmarkEquality, int] {
	b.Helper()
	schema := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional }))
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkEquality, int) {
		value := n
		if highCardinality {
			value %= benchmarkCardinality
		}
		return benchmarkEquality{optional: benchmarkPtr(value)}, n
	})
}

func BenchmarkEq(b *testing.B) {
	for _, high := range []bool{false, true} {
		cardinality := "LowCardinality"
		if high {
			cardinality = "HighCardinality"
		}
		b.Run(cardinality+"/Hit", func(b *testing.B) {
			ix := benchmarkEqIndex(b, high)
			query := benchmarkEquality{optional: benchmarkPtr(42)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
		b.Run(cardinality+"/Miss", func(b *testing.B) {
			ix := benchmarkEqIndex(b, high)
			query := benchmarkEquality{optional: benchmarkPtr(benchmarkEntries + 1)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
	b.Run("Wildcard", func(b *testing.B) {
		ix := buildGenerated(b, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })), benchmarkEntries,
			func(n int) (benchmarkEquality, int) { return benchmarkEquality{}, n })
		query := benchmarkEquality{optional: benchmarkPtr(42)}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkIntResult = benchmarkIntResult[:0]
			ix.Search(query, &benchmarkIntResult)
		}
	})
}

func BenchmarkLocalCreation(b *testing.B) {
	ix := benchmarkEqIndex(b, true)
	b.Run("Cold", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkLocalResult = ix.Local()
		}
	})
	b.Run("Primed", func(b *testing.B) {
		query := benchmarkEquality{optional: benchmarkPtr(42)}
		var dst []int
		b.ReportAllocs()
		for range b.N {
			local := ix.Local()
			dst = dst[:0]
			local.Search(query, &dst)
			benchmarkLocalResult = local
		}
	})
}

func BenchmarkLocalEqualityReuse(b *testing.B) {
	type query struct{ store, region *int }
	ix := buildGenerated(b, ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(v query) *int { return v.store })),
		ruleix.Include(ruleix.GetterFromPointer(func(v query) *int { return v.region })),
	), benchmarkEntries, func(n int) (query, int) {
		switch {
		case n < 10:
			return query{store: benchmarkPtr(10), region: benchmarkPtr(20)}, n
		case n < 20:
			return query{store: benchmarkPtr(10), region: benchmarkPtr(30)}, n
		case n < 2_000:
			return query{region: benchmarkPtr(99)}, n
		case n < 4_000:
			return query{store: benchmarkPtr(11)}, n
		case n < 7_000:
			return query{store: benchmarkPtr(10), region: benchmarkPtr(99)}, n
		default:
			return query{store: benchmarkPtr(11), region: benchmarkPtr(20 + 10*(n&1))}, n
		}
	})
	store, regions := 10, [2]int{20, 30}

	for _, local := range []bool{false, true} {
		name := "Index"
		if local {
			name = "Local"
		}
		b.Run(name, func(b *testing.B) {
			searcher := ix.Local()
			var dst []int
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				q := query{store: &store, region: &regions[n&1]}
				dst = dst[:0]
				if local {
					searcher.Search(q, &dst)
				} else {
					ix.Search(q, &dst)
				}
			}
		})
	}
}

func benchmarkOrderedIndex(b *testing.B, kind string) *ruleix.Index[benchmarkRange, int] {
	b.Helper()
	var schema ruleix.Rule[benchmarkRange]
	switch kind {
	case "GTE":
		schema = ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])
	case "LTE":
		schema = ruleix.LessOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])
	case "CompareBy":
		schema = ruleix.CompareBy(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), ruleix.GetterFromPointer(func(v benchmarkRange) *ruleix.Operator { return v.operator }), cmp.Compare[int])
	default:
		b.Fatalf("unknown ordered benchmark %q", kind)
	}
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkRange, int) {
		return benchmarkRange{operator: ptr(ruleix.OperatorGTE), value: benchmarkPtr(n)}, n
	})
}

func BenchmarkOrdered(b *testing.B) {
	positions := []struct {
		name  string
		value int
	}{{"Start", 0}, {"Middle", benchmarkEntries / 2}, {"End", benchmarkEntries - 1}}
	for _, kind := range []string{"GTE", "LTE", "CompareBy"} {
		for _, position := range positions {
			b.Run(kind+"/"+position.name, func(b *testing.B) {
				ix := benchmarkOrderedIndex(b, kind)
				query := benchmarkRange{operator: ptr(ruleix.OperatorGTE), value: benchmarkPtr(position.value)}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkIntResult = benchmarkIntResult[:0]
					ix.Search(query, &benchmarkIntResult)
				}
			})
		}
	}
}

func BenchmarkLocalOrderedReuse(b *testing.B) {
	ix := benchmarkOrderedIndex(b, "GTE")
	modes := []struct {
		name  string
		value func(int) int
	}{
		{"Repeat", func(int) int { return benchmarkEntries / 2 }},
		{"Alternate", func(n int) int { return benchmarkEntries/2 + (n & 1) }},
		{"HotWithInterlopers", func(n int) int { return benchmarkEntries/2 + [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) int { return n % benchmarkEntries }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					value := mode.value(n)
					query := benchmarkRange{value: &value}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkLocalCompareByReuse(b *testing.B) {
	ix := benchmarkOrderedIndex(b, "CompareBy")
	modes := []struct {
		name  string
		value func(int) int
	}{
		{"Repeat", func(int) int { return benchmarkEntries / 2 }},
		{"Alternate", func(n int) int { return benchmarkEntries/2 + (n & 1) }},
		{"HotWithInterlopers", func(n int) int { return benchmarkEntries/2 + [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) int { return n % benchmarkEntries }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					value := mode.value(n)
					query := benchmarkRange{value: &value}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkBetweenManyUniqueBounds(b *testing.B) {
	ix := buildGenerated(b, ruleix.Between(ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.from }), ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.until }), cmp.Compare[int]), benchmarkEntries, func(n int) (benchmarkInterval, int) {
		return benchmarkInterval{from: benchmarkPtr(n), until: benchmarkPtr(benchmarkEntries*2 - n)}, n
	})
	query := benchmarkInterval{
		from:  benchmarkPtr(benchmarkEntries / 2),
		until: benchmarkPtr(benchmarkEntries + benchmarkEntries/2),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkIntResult = benchmarkIntResult[:0]
		ix.Search(query, &benchmarkIntResult)
	}
}

func BenchmarkBetweenNarrowIntersection(b *testing.B) {
	from := func(v benchmarkInterval) *int { return v.from }
	until := func(v benchmarkInterval) *int { return v.until }
	for _, tt := range []struct {
		name   string
		schema ruleix.Rule[benchmarkInterval]
	}{
		{"Specialized", ruleix.Between(ruleix.GetterFromPointer(from), ruleix.GetterFromPointer(until), cmp.Compare[int])},
		{"Composed", ruleix.All(
			ruleix.GreaterOrEqual(ruleix.GetterFromPointer(from), cmp.Compare[int]),
			ruleix.LessOrEqual(ruleix.GetterFromPointer(until), cmp.Compare[int]),
		)},
	} {
		b.Run(tt.name, func(b *testing.B) {
			ix := buildGenerated(b, tt.schema, benchmarkEntries, func(n int) (benchmarkInterval, int) {
				switch {
				case n == 0:
					return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(10_000)}, n
				case n < benchmarkEntries/2:
					return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(6_000)}, n
				default:
					return benchmarkInterval{from: benchmarkPtr(6_000), until: benchmarkPtr(10_000)}, n
				}
			})
			query := benchmarkInterval{from: benchmarkPtr(5_000), until: benchmarkPtr(7_000)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkLocalBetweenReuse(b *testing.B) {
	ix := buildGenerated(b, ruleix.Between(ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.from }), ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.until }), cmp.Compare[int]), benchmarkEntries, func(n int) (benchmarkInterval, int) {
		switch {
		case n == 0:
			return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(10_000)}, n
		case n < benchmarkEntries/2:
			return benchmarkInterval{from: benchmarkPtr(0), until: benchmarkPtr(6_000)}, n
		default:
			return benchmarkInterval{from: benchmarkPtr(6_000), until: benchmarkPtr(10_000)}, n
		}
	})
	modes := []struct {
		name  string
		value func(int) (int, int)
	}{
		{"Repeat", func(int) (int, int) { return 5_000, 7_000 }},
		{"Alternate", func(n int) (int, int) { return 5_000, 7_000 + (n & 1) }},
		{"HotWithInterlopers", func(n int) (int, int) { return 5_000, 7_000 + [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) (int, int) { return n % 5_000, 7_000 }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					from, until := mode.value(n)
					query := benchmarkInterval{from: &from, until: &until}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkLocalExcludeReuse(b *testing.B) {
	const entries = 32
	ix := buildGenerated(b, ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value })), entries, func(n int) (benchmarkRange, int) {
		value := n & 1
		return benchmarkRange{value: benchmarkPtr(value)}, n
	})
	modes := []struct {
		name  string
		value func(int) int
	}{
		{"Repeat", func(int) int { return 0 }},
		{"Alternate", func(n int) int { return n & 1 }},
		{"HotWithInterlopers", func(n int) int { return [...]int{0, 1, 0, 2}[n&3] }},
		{"Churn", func(n int) int { return n }},
	}
	for _, mode := range modes {
		for _, local := range []bool{false, true} {
			name := mode.name + "/Index"
			if local {
				name = mode.name + "/Local"
			}
			b.Run(name, func(b *testing.B) {
				searcher := ix.Local()
				var dst []int
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					value := mode.value(n)
					query := benchmarkRange{value: &value}
					dst = dst[:0]
					if local {
						searcher.Search(query, &dst)
					} else {
						ix.Search(query, &dst)
					}
				}
			})
		}
	}
}

func BenchmarkBetweenSelectiveSide(b *testing.B) {
	from := func(v benchmarkInterval) *int { return v.from }
	until := func(v benchmarkInterval) *int { return v.until }
	for _, tt := range []struct {
		name   string
		schema ruleix.Rule[benchmarkInterval]
	}{
		{"Specialized", ruleix.Between(ruleix.GetterFromPointer(from), ruleix.GetterFromPointer(until), cmp.Compare[int])},
		{"Composed", ruleix.All(
			ruleix.GreaterOrEqual(ruleix.GetterFromPointer(from), cmp.Compare[int]),
			ruleix.LessOrEqual(ruleix.GetterFromPointer(until), cmp.Compare[int]),
		)},
	} {
		b.Run(tt.name, func(b *testing.B) {
			ix := buildGenerated(b, tt.schema, benchmarkEntries, func(n int) (benchmarkInterval, int) {
				return benchmarkInterval{from: benchmarkPtr(n), until: benchmarkPtr(benchmarkEntries + n)}, n
			})
			query := benchmarkInterval{from: benchmarkPtr(31), until: benchmarkPtr(benchmarkEntries)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkFilterWildcard(b *testing.B) {
	tests := []struct {
		name   string
		schema ruleix.Rule[benchmarkRange]
	}{
		{"GTE", ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])},
		{"LTE", ruleix.LessOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int])},
		{"CompareBy", ruleix.CompareBy(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), ruleix.GetterFromPointer(func(v benchmarkRange) *ruleix.Operator { return v.operator }), cmp.Compare[int])},
		{"Not", ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }))},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ix := buildGenerated(b, tt.schema, benchmarkEntries,
				func(n int) (benchmarkRange, int) { return benchmarkRange{}, n })
			op := ruleix.OperatorGTE
			query := benchmarkRange{operator: &op, value: benchmarkPtr(42)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func benchmarkAllIndex(b *testing.B, nested bool) *ruleix.Index[benchmarkAllValue, int] {
	b.Helper()
	eqA := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.a }))
	eqB := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.b }))
	eqC := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.c }))
	eqD := ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.d }))
	var schema ruleix.Rule[benchmarkAllValue]
	if nested {
		schema = ruleix.All(eqA, ruleix.All(eqB, ruleix.All(eqC, eqD)))
	} else {
		schema = ruleix.All(eqA, eqB, eqC, eqD)
	}
	return buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkAllValue, int) {
		return benchmarkAllValue{
			benchmarkPtr(n % 2),
			benchmarkPtr(n % 5),
			benchmarkPtr(n % 10),
			benchmarkPtr(n % benchmarkCardinality),
		}, n
	})
}

func BenchmarkAll(b *testing.B) {
	for _, nested := range []bool{false, true} {
		name := "Flat"
		if nested {
			name = "Nested"
		}
		b.Run(name, func(b *testing.B) {
			ix := benchmarkAllIndex(b, nested)
			query := benchmarkAllValue{benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkLocalAllCardinalityReuse(b *testing.B) {
	ix := benchmarkAllIndex(b, false)
	local := ix.Local()
	query := benchmarkAllValue{benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1)}
	local.Search(query, &benchmarkIntResult)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkIntResult = benchmarkIntResult[:0]
		local.Search(query, &benchmarkIntResult)
	}
}

// BenchmarkAllCardinalityOrder keeps the expensive ordered filter first in the
// schema on purpose. It measures the value of query-dependent candidate sizing:
// replacing it with fixed schema order makes both cases materialize the broad
// ordered result before discovering the empty or narrow equality result.
func BenchmarkAllCardinalityOrder(b *testing.B) {
	schema := ruleix.All(
		ruleix.Greater(ruleix.GetterFromPointer(func(v benchmarkCardinalityOrderValue) *int { return v.threshold }), cmp.Compare[int]),
		ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkCardinalityOrderValue) *int { return v.group })),
	)
	ix := buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkCardinalityOrderValue, int) {
		return benchmarkCardinalityOrderValue{threshold: benchmarkPtr(n), group: benchmarkPtr(n % 100)}, n
	})

	for _, benchmark := range []struct {
		name  string
		group int
	}{
		{name: "ExpensiveBeforeEmpty", group: benchmarkEntries + 1},
		{name: "ExpensiveBeforeNarrow", group: 1},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			query := benchmarkCardinalityOrderValue{
				threshold: benchmarkPtr(benchmarkEntries - 1),
				group:     benchmarkPtr(benchmark.group),
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

// BenchmarkAllSkewedEquality prevents the number of unique map keys from being
// mistaken for query selectivity. The many-values filter is usually selective,
// while the two-value filter is either almost universal or uniquely selective
// depending on the queried value.
func BenchmarkAllSkewedEquality(b *testing.B) {
	schema := ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkSkewedEqualityValue) *int { return v.manyValues })),
		ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkSkewedEqualityValue) *int { return v.skewed })),
	)
	ix := buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkSkewedEqualityValue, int) {
		skewed := 0
		if n == benchmarkEntries-1 {
			skewed = 1
		}
		return benchmarkSkewedEqualityValue{
			manyValues: benchmarkPtr(n % benchmarkCardinality),
			skewed:     benchmarkPtr(skewed),
		}, n
	})

	for _, benchmark := range []struct {
		name   string
		skewed int
	}{
		{name: "ManyKeysSelective", skewed: 0},
		{name: "FewKeysSelective", skewed: 1},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			query := benchmarkSkewedEqualityValue{
				manyValues: benchmarkPtr(benchmarkEntries % benchmarkCardinality),
				skewed:     benchmarkPtr(benchmark.skewed),
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(query, &benchmarkIntResult)
			}
		})
	}
}

func BenchmarkAllWithExclusions(b *testing.B) {
	for _, candidates := range []int{10, 100} {
		b.Run(fmt.Sprintf("Candidates%d", candidates), func(b *testing.B) {
			schema := ruleix.All(
				ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkExcludeValue) *int { return v.include })),
				ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkExcludeValue) *int { return v.excludeA })),
				ruleix.Exclude(ruleix.GetterFromPointer(func(v benchmarkExcludeValue) *int { return v.excludeB })),
			)
			index := buildGenerated(b, schema, benchmarkEntries, func(n int) (benchmarkExcludeValue, int) {
				return benchmarkExcludeValue{
					include:  benchmarkPtr(n % (benchmarkEntries / candidates)),
					excludeA: benchmarkPtr(n % 10),
					excludeB: benchmarkPtr(n % 20),
				}, n
			})
			query := benchmarkExcludeValue{include: benchmarkPtr(1), excludeA: benchmarkPtr(2), excludeB: benchmarkPtr(3)}
			for _, local := range []bool{false, true} {
				name := "Index"
				if local {
					name = "Local"
				}
				b.Run(name, func(b *testing.B) {
					searcher := index.Local()
					var result []int
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						result = result[:0]
						if local {
							searcher.Search(query, &result)
						} else {
							index.Search(query, &result)
						}
					}
				})
			}
		})
	}
}

func BenchmarkParallelSearch(b *testing.B) {
	ix := benchmarkAllIndex(b, true)
	query := benchmarkAllValue{benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1), benchmarkPtr(1)}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var dst []int
		for pb.Next() {
			dst = dst[:0]
			ix.Search(query, &dst)
			if len(dst) == 0 {
				b.Error("unexpected empty result")
			}
		}
	})
}

func BenchmarkBuildIndex(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ix := buildGenerated(b, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })), benchmarkEntries,
			func(n int) (benchmarkEquality, string) {
				return benchmarkEquality{optional: benchmarkPtr(n)}, fmt.Sprintf("modifier-%d", n)
			})
		benchmarkStringResult = benchmarkStringResult[:0]
		ix.Search(benchmarkEquality{optional: benchmarkPtr(benchmarkEntries / 2)}, &benchmarkStringResult)
	}
	b.ReportMetric(benchmarkEntries, "rules/op")
}

func BenchmarkBuildEqualityCardinality(b *testing.B) {
	for _, cardinality := range []int{1, 2, 4, 8, 16, 32, 100} {
		b.Run(fmt.Sprintf("IDsPerValue/%d", cardinality), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ix := buildGenerated(b, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })), benchmarkEntries,
					func(n int) (benchmarkEquality, int) {
						return benchmarkEquality{optional: benchmarkPtr(n / cardinality)}, n
					})
				benchmarkIntResult = benchmarkIntResult[:0]
				ix.Search(benchmarkEquality{optional: benchmarkPtr(0)}, &benchmarkIntResult)
			}
			b.ReportMetric(float64(benchmarkEntries/cardinality), "values/op")
		})
	}
}

func BenchmarkBuildOrderedIndex(b *testing.B) {
	values := make([]int, benchmarkEntries)
	for n := range values {
		values[n] = n
	}
	queryValue := benchmarkEntries / 2
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ix := buildGenerated(b, ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int]), benchmarkEntries, func(n int) (benchmarkRange, int) { return benchmarkRange{value: &values[n]}, n })
		benchmarkIntResult = benchmarkIntResult[:0]
		ix.Search(benchmarkRange{value: &queryValue}, &benchmarkIntResult)
	}
	b.ReportMetric(benchmarkEntries, "rules/op")
}
