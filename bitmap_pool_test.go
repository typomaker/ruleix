package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type bitmapPoolBenchmarkConstraint struct {
	a *int
	b *int
	c *int
	d *int
}

func TestAppendBitmapValuesSupportsScalarAndBatchIteration(t *testing.T) {
	values := make([]int, manyIteratorCardinalityThreshold+1)
	for i := range values {
		values[i] = i * 2
	}

	for _, size := range []int{manyIteratorCardinalityThreshold - 1, manyIteratorCardinalityThreshold + 1} {
		bits := roaring.New()
		bits.AddRange(0, uint64(size))
		result := appendBitmapValues(bits, values, nil)
		require.Len(t, result, size)
		require.Equal(t, 0, result[0])
		require.Equal(t, (size-1)*2, result[size-1])
	}
}

func TestBitmapPoolReturnsEmptyBitmapAfterPut(t *testing.T) {
	pool := newBitmapPool()
	bits := pool.get()
	bits.AddRange(0, 1_000)
	require.LessOrEqual(t, bits.GetSizeInBytes(), uint64(maxPooledBitmapBytes))

	pool.put(bits)

	reused := pool.get()
	require.True(t, reused.IsEmpty())
	reused.Add(42)
	require.True(t, reused.Contains(42))
}

func TestBitmapPoolDiscardsOversizedBitmap(t *testing.T) {
	pool := newBitmapPool()
	bits := pool.get()
	// One value per high-16-bit key creates enough containers to exceed the
	// pool limit without requiring a large cardinality.
	for id := uint32(0); bits.GetSizeInBytes() <= maxPooledBitmapBytes; id++ {
		bits.Add(id << 16)
	}
	require.Greater(t, bits.GetSizeInBytes(), uint64(maxPooledBitmapBytes))

	pool.put(bits)

	require.NotSame(t, bits, pool.get())
}

// BenchmarkBitmapPoolRareWide models a large scratch result appearing among
// predominantly narrow searches. It guards both narrow reuse and the cost of
// rejecting the occasional oversized bitmap.
func BenchmarkBitmapPoolRareWide(b *testing.B) {
	pool := newBitmapPool()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bits := pool.get()
		if i%128 == 0 {
			for id := uint32(0); id < 8_192; id++ {
				bits.Add(id << 16)
			}
		} else {
			bits.Add(uint32(i))
		}
		pool.put(bits)
	}
}

// BenchmarkBitmapPoolParallelSearch compares the shared Index scratch pool
// with an otherwise identical search that starts with an empty pool. RunParallel
// exercises sync.Pool's per-P reuse and contention behavior instead of only
// measuring a serial best case.
func BenchmarkBitmapPoolParallelSearch(b *testing.B) {
	ptr := func(value int) *int { return &value }
	schema := All(
		Include(GetterFromPointer(func(value bitmapPoolBenchmarkConstraint) *int { return value.a })),
		Include(GetterFromPointer(func(value bitmapPoolBenchmarkConstraint) *int { return value.b })),
		Include(GetterFromPointer(func(value bitmapPoolBenchmarkConstraint) *int { return value.c })),
		Include(GetterFromPointer(func(value bitmapPoolBenchmarkConstraint) *int { return value.d })),
	)
	const entries = 100_000
	constraints := make([]bitmapPoolBenchmarkConstraint, entries)
	ids := make([]int, entries)
	for id := range entries {
		constraints[id] = bitmapPoolBenchmarkConstraint{
			a: ptr(id % 2),
			b: ptr(id % 5),
			c: ptr(id % 10),
			d: ptr(id % 100),
		}
		ids[id] = id
	}
	index, err := New[bitmapPoolBenchmarkConstraint, int](schema).Build(Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	query := bitmapPoolBenchmarkConstraint{a: ptr(1), b: ptr(1), c: ptr(1), d: ptr(1)}

	for _, benchmark := range []struct {
		name   string
		search func(*[]int)
	}{
		{name: "Reuse", search: func(dst *[]int) { index.Search(query, dst) }},
		{name: "Fresh", search: func(dst *[]int) { index.search(query, dst, newBitmapPool()) }},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				matches := make([]int, 0, entries/100)
				for pb.Next() {
					matches = matches[:0]
					benchmark.search(&matches)
					if len(matches) != entries/100 {
						b.Errorf("got %d matches", len(matches))
						return
					}
				}
			})
		})
	}
}
