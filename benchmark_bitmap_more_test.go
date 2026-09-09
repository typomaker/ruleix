//nolint:lll // Migration benchmarks keep legacy pointer getters inline.
package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/typomaker/ruleix"
)

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
